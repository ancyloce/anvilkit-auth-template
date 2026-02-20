package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"anvilkit-auth-template/services/auth-api/internal/auth/crypto"
)

type Store struct{ DB *pgxpool.Pool }

type BootstrapResult struct {
	UserID   string
	TenantID string
	Email    string
}

type RegisteredUser struct {
	ID    string
	Email string
}

type LoginUser struct {
	ID    string
	Email string
}

func (s *Store) Register(ctx context.Context, email, password string, bcryptCost int) (*RegisteredUser, error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			_ = rbErr
		}
	}()

	id := uuid.NewString()
	h, err := crypto.HashPassword(password, bcryptCost)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `insert into users(id,email,status,created_at,updated_at) values($1,$2,1,now(),now())`, id, email); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `insert into user_password_credentials(user_id,password_hash,updated_at) values($1,$2,now())`, id, h); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &RegisteredUser{ID: id, Email: email}, nil
}

func (s *Store) Bootstrap(ctx context.Context, email, password, tenantName string, bcryptCost int) (*BootstrapResult, error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			_ = rbErr
		}
	}()

	uid := ""
	var pwdHash *string
	err = tx.QueryRow(ctx, `
select u.id, upc.password_hash
from users u
left join user_password_credentials upc on upc.user_id = u.id
where u.email=$1`, email).Scan(&uid, &pwdHash)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		uid = uuid.NewString()
		h, hErr := crypto.HashPassword(password, bcryptCost)
		if hErr != nil {
			return nil, hErr
		}
		if _, err = tx.Exec(ctx, `insert into users(id,email,status,created_at,updated_at) values($1,$2,1,now(),now())`, uid, email); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `insert into user_password_credentials(user_id,password_hash,updated_at) values($1,$2,now())`, uid, h); err != nil {
			return nil, err
		}
	} else {
		if pwdHash == nil || crypto.VerifyPassword(*pwdHash, password) != nil {
			return nil, errors.New("invalid_password")
		}
	}

	tid := uuid.NewString()
	if _, err = tx.Exec(ctx, `insert into tenants(id,name,created_at) values($1,$2,now())`, tid, tenantName); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `insert into tenant_users(tenant_id,user_id,created_at) values($1,$2,now())`, tid, uid); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `insert into user_roles(tenant_id,user_id,role,created_at) values($1,$2,'tenant_admin',now())`, tid, uid); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &BootstrapResult{UserID: uid, TenantID: tid, Email: email}, nil
}

func (s *Store) Login(ctx context.Context, email, password, tenantID string) (string, error) {
	var uid, hash string
	err := s.DB.QueryRow(ctx, `
select u.id, upc.password_hash
from users u
join user_password_credentials upc on upc.user_id = u.id
join tenant_users tu on tu.user_id = u.id and tu.tenant_id=$2
where u.email=$1`, email, tenantID).Scan(&uid, &hash)
	if err != nil {
		return "", err
	}
	if crypto.VerifyPassword(hash, password) != nil {
		return "", errors.New("invalid_password")
	}
	return uid, nil
}

func (s *Store) SaveRefreshToken(ctx context.Context, token, userID, tenantID string, exp time.Time) error {
	h := sha256.Sum256([]byte(token))
	_, err := s.DB.Exec(ctx, `insert into refresh_tokens(token_hash,user_id,tenant_id,expires_at,created_at) values($1,$2,$3,$4,now())`, hex.EncodeToString(h[:]), userID, tenantID, exp)
	return err
}

func (s *Store) RotateRefreshToken(ctx context.Context, oldToken, newToken string, exp time.Time) (string, string, error) {
	oldH := sha256.Sum256([]byte(oldToken))
	newH := sha256.Sum256([]byte(newToken))
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", err
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			_ = rbErr
		}
	}()

	var uid, tid string
	err = tx.QueryRow(ctx, `
select user_id,tenant_id from refresh_tokens
where token_hash=$1 and revoked_at is null and expires_at > now()`, hex.EncodeToString(oldH[:])).Scan(&uid, &tid)
	if err != nil {
		return "", "", err
	}
	if _, err = tx.Exec(ctx, `update refresh_tokens set revoked_at=now() where token_hash=$1`, hex.EncodeToString(oldH[:])); err != nil {
		return "", "", err
	}
	if _, err = tx.Exec(ctx, `insert into refresh_tokens(token_hash,user_id,tenant_id,expires_at,created_at) values($1,$2,$3,$4,now())`, hex.EncodeToString(newH[:]), uid, tid, exp); err != nil {
		return "", "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return uid, tid, nil
}

func (s *Store) RevokeRefreshToken(ctx context.Context, token string) error {
	h := sha256.Sum256([]byte(token))
	_, err := s.DB.Exec(ctx, `update refresh_tokens set revoked_at=now() where token_hash=$1 and revoked_at is null`, hex.EncodeToString(h[:]))
	return err
}

// VerifyEmailPassword looks up a user by email, checks the password and
// confirms the account is active (status=1). Returns the user on success or
// an error on any failure (not found, wrong password, inactive).
func (s *Store) VerifyEmailPassword(ctx context.Context, email, password string) (*LoginUser, error) {
	var id, emailOut string
	var status int
	var pwdHash string
	err := s.DB.QueryRow(ctx, `
select u.id, u.email, u.status, upc.password_hash
from users u
join user_password_credentials upc on upc.user_id = u.id
where u.email=$1`, email).Scan(&id, &emailOut, &status, &pwdHash)
	if err != nil {
		return nil, err
	}
	if status != 1 {
		return nil, errors.New("user_inactive")
	}
	if crypto.VerifyPassword(pwdHash, password) != nil {
		return nil, errors.New("invalid_password")
	}
	return &LoginUser{ID: id, Email: emailOut}, nil
}

// CreateRefreshSession inserts a new row into refresh_sessions.
func (s *Store) CreateRefreshSession(ctx context.Context, id, userID, tokenHash, userAgent, ip string, exp time.Time) error {
	_, err := s.DB.Exec(ctx, `
insert into refresh_sessions(id, user_id, token_hash, user_agent, ip, expires_at, created_at)
values($1, $2, $3, $4, $5, $6, now())`,
		id, userID, tokenHash, userAgent, ip, exp)
	return err
}
