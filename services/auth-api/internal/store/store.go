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

// Sentinel errors returned by RotateRefreshToken so callers can produce
// precise error reasons without re-querying the database.
var (
	ErrRefreshExpired = errors.New("refresh_expired")
	ErrRefreshRevoked = errors.New("session_revoked")
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
	ID           string
	Email        string
	Status       int16
	PasswordHash string
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

func (s *Store) GetLoginUserByEmail(ctx context.Context, email string) (*LoginUser, error) {
	var user LoginUser
	err := s.DB.QueryRow(ctx, `
select u.id, u.email, u.status, upc.password_hash
from users u
join user_password_credentials upc on upc.user_id = u.id
where u.email=$1`, email).Scan(&user.ID, &user.Email, &user.Status, &user.PasswordHash)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) SaveRefreshSession(ctx context.Context, token, userID string, exp time.Time, userAgent, ip string) error {
	h := sha256.Sum256([]byte(token))
	_, err := s.DB.Exec(
		ctx,
		`insert into refresh_sessions(id,user_id,token_hash,user_agent,ip,expires_at,created_at) values($1,$2,$3,$4,$5,$6,now())`,
		uuid.NewString(),
		userID,
		hex.EncodeToString(h[:]),
		userAgent,
		ip,
		exp,
	)
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

	// Lock the row to prevent concurrent replay attacks.
	var uid string
	var expiresAt time.Time
	var revokedAt *time.Time
	err = tx.QueryRow(ctx, `
select user_id, expires_at, revoked_at from refresh_sessions
where token_hash=$1
for update`, hex.EncodeToString(oldH[:])).Scan(&uid, &expiresAt, &revokedAt)
	if err != nil {
		// Not found at all – return ErrNoRows so caller maps to 401.
		return "", "", err
	}

	// Re-check state under lock to handle concurrent replays.
	if revokedAt != nil {
		return "", "", ErrRefreshRevoked
	}
	if expiresAt.Before(time.Now()) {
		return "", "", ErrRefreshExpired
	}

	newID := uuid.NewString()
	if _, err = tx.Exec(ctx, `
insert into refresh_sessions(id,user_id,token_hash,expires_at,created_at)
values($1,$2,$3,$4,now())`, newID, uid, hex.EncodeToString(newH[:]), exp); err != nil {
		return "", "", err
	}

	if _, err = tx.Exec(ctx, `
update refresh_sessions
set revoked_at=now(), replaced_by=$2
where token_hash=$1`, hex.EncodeToString(oldH[:]), newID); err != nil {
		return "", "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return uid, "", nil
}

func (s *Store) RevokeRefreshToken(ctx context.Context, token string) error {
	h := sha256.Sum256([]byte(token))
	_, err := s.DB.Exec(ctx, `update refresh_sessions set revoked_at=now() where token_hash=$1 and revoked_at is null`, hex.EncodeToString(h[:]))
	return err
}
