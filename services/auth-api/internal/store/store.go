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

var (
	ErrRefreshSessionNotFound    = errors.New("refresh_session_not_found")
	ErrRefreshExpired            = errors.New("refresh_expired")
	ErrRefreshSessionRevoked     = errors.New("session_revoked")
	ErrBootstrapPasswordMismatch = errors.New("bootstrap_password_mismatch")
	ErrTenantNameConflict        = errors.New("tenant_name_conflict")
	ErrNotInTenant               = errors.New("not_in_tenant")
)

type BootstrapResult struct {
	UserID     string
	UserEmail  string
	TenantID   string
	TenantName string
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
			return nil, ErrBootstrapPasswordMismatch
		}
	}

	var tenantExists bool
	if err = tx.QueryRow(ctx, `select exists(select 1 from tenants where name=$1)`, tenantName).Scan(&tenantExists); err != nil {
		return nil, err
	}
	if tenantExists {
		return nil, ErrTenantNameConflict
	}

	tid := uuid.NewString()
	if _, err = tx.Exec(ctx, `insert into tenants(id,name,created_at) values($1,$2,now())`, tid, tenantName); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `insert into tenant_users(tenant_id,user_id,role,created_at) values($1,$2,'owner',now())`, tid, uid); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &BootstrapResult{UserID: uid, UserEmail: email, TenantID: tid, TenantName: tenantName}, nil
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
	oldHash := hex.EncodeToString(oldH[:])
	newHash := hex.EncodeToString(newH[:])
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", err
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			_ = rbErr
		}
	}()

	var (
		uid       string
		expiresAt time.Time
		revokedAt *time.Time
	)
	err = tx.QueryRow(ctx, `
select user_id, expires_at, revoked_at
from refresh_sessions
where token_hash=$1
for update`, oldHash).Scan(&uid, &expiresAt, &revokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrRefreshSessionNotFound
		}
		return "", "", err
	}
	if revokedAt != nil {
		return "", "", ErrRefreshSessionRevoked
	}
	if expiresAt.Before(time.Now()) {
		return "", "", ErrRefreshExpired
	}

	newID := uuid.NewString()
	if _, err = tx.Exec(ctx, `
insert into refresh_sessions(id,user_id,token_hash,expires_at,created_at)
values($1,$2,$3,$4,now())`, newID, uid, newHash, exp); err != nil {
		return "", "", err
	}

	if _, err = tx.Exec(ctx, `
update refresh_sessions
set revoked_at=now(), replaced_by=$2
where token_hash=$1 and revoked_at is null`, oldHash, newID); err != nil {
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

func (s *Store) RevokeAllRefreshTokensByUser(ctx context.Context, userID string) (int64, error) {
	ct, err := s.DB.Exec(ctx, `update refresh_sessions set revoked_at=now() where user_id=$1 and revoked_at is null`, userID)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

func (s *Store) EnsureUserInTenant(ctx context.Context, userID, tenantID string) error {
	var exists bool
	if err := s.DB.QueryRow(ctx, `select exists(select 1 from tenant_users where tenant_id=$1 and user_id=$2)`, tenantID, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotInTenant
	}
	return nil
}
