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

	"anvilkit-auth-template/modules/common-go/pkg/email"
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
	ErrInvalidVerificationOTP    = errors.New("invalid_verification_otp")
	ErrInvalidMagicLink          = errors.New("invalid_magic_link")
	ErrVerificationExpired       = errors.New("verification_expired")
	ErrPendingRegistrationGone   = errors.New("pending_registration_not_found")
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

type CreateVerificationParams struct {
	UserID     string
	OTP        string
	MagicToken string
	ExpiresAt  time.Time
}

type CreateVerificationResult struct {
	EmailRecordID string
}

type RegisterWithVerificationResult struct {
	User          RegisteredUser
	EmailRecordID string
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
	if err = insertRegisteredUserWithPassword(ctx, tx, id, email, password, bcryptCost); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &RegisteredUser{ID: id, Email: email}, nil
}

func (s *Store) RegisterWithVerification(
	ctx context.Context,
	emailAddr, password string,
	bcryptCost int,
	otp, magicToken string,
	expiresAt time.Time,
) (*RegisterWithVerificationResult, error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			_ = rbErr
		}
	}()

	userID := uuid.NewString()
	if err = insertRegisteredUserWithPassword(ctx, tx, userID, emailAddr, password, bcryptCost); err != nil {
		return nil, err
	}
	emailRecordID, err := createVerificationTx(ctx, tx, CreateVerificationParams{
		UserID:     userID,
		OTP:        otp,
		MagicToken: magicToken,
		ExpiresAt:  expiresAt,
	})
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &RegisterWithVerificationResult{
		User:          RegisteredUser{ID: userID, Email: emailAddr},
		EmailRecordID: emailRecordID,
	}, nil
}

func (s *Store) CreateVerification(ctx context.Context, params CreateVerificationParams) (*CreateVerificationResult, error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			_ = rbErr
		}
	}()

	emailRecordID, err := createVerificationTx(ctx, tx, params)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &CreateVerificationResult{EmailRecordID: emailRecordID}, nil
}

func (s *Store) VerifyEmailOTP(ctx context.Context, emailAddr, otp string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			_ = rbErr
		}
	}()

	tokenHash := email.HashToken(otp)
	var (
		verificationID string
		userID         string
		expiresAt      time.Time
	)
	err = tx.QueryRow(ctx, `
select ev.id, ev.user_id, ev.expires_at
from email_verifications ev
join users u on u.id = ev.user_id
where u.email = $1
  and ev.token_type = 'otp'
  and ev.token_hash = $2
  and ev.verified_at is null
for update`,
		emailAddr,
		tokenHash,
	).Scan(&verificationID, &userID, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidVerificationOTP
		}
		return err
	}

	if !expiresAt.After(now) {
		return ErrVerificationExpired
	}

	if _, err = tx.Exec(ctx, `update email_verifications set verified_at=now() where id=$1`, verificationID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `update users set status=1,email_verified_at=coalesce(email_verified_at,now()),updated_at=now() where id=$1`, userID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) VerifyMagicLinkToken(ctx context.Context, magicToken string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			_ = rbErr
		}
	}()

	tokenHash := email.HashToken(magicToken)
	var (
		verificationID string
		userID         string
		expiresAt      time.Time
	)
	err = tx.QueryRow(ctx, `
select id, user_id, expires_at
from email_verifications
where token_type = 'magic_link'
  and token_hash = $1
  and verified_at is null
for update`,
		tokenHash,
	).Scan(&verificationID, &userID, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidMagicLink
		}
		return err
	}

	if !expiresAt.After(now) {
		return ErrVerificationExpired
	}

	if _, err = tx.Exec(ctx, `update email_verifications set verified_at=now() where id=$1`, verificationID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `update users set status=1,email_verified_at=coalesce(email_verified_at,now()),updated_at=now() where id=$1`, userID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) CleanupPendingRegistration(ctx context.Context, userID string) error {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			_ = rbErr
		}
	}()

	if _, err = tx.Exec(ctx, `delete from email_records where user_id=$1`, userID); err != nil {
		return err
	}
	ct, err := tx.Exec(ctx, `delete from users where id=$1 and status=0 and email_verified_at is null`, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrPendingRegistrationGone
	}
	return tx.Commit(ctx)
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

func insertRegisteredUserWithPassword(ctx context.Context, tx pgx.Tx, userID, emailAddr, password string, bcryptCost int) error {
	hashedPassword, err := crypto.HashPassword(password, bcryptCost)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `insert into users(id,email,status,created_at,updated_at) values($1,$2,0,now(),now())`, userID, emailAddr); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `insert into user_password_credentials(user_id,password_hash,updated_at) values($1,$2,now())`, userID, hashedPassword); err != nil {
		return err
	}
	return nil
}

func createVerificationTx(ctx context.Context, tx pgx.Tx, params CreateVerificationParams) (string, error) {
	var recipientEmail string
	if err := tx.QueryRow(ctx, `select email from users where id=$1`, params.UserID).Scan(&recipientEmail); err != nil {
		return "", err
	}

	otpHash := email.HashToken(params.OTP)
	magicLinkHash := email.HashToken(params.MagicToken)
	if _, err := tx.Exec(
		ctx,
		`insert into email_verifications(id,user_id,token_hash,token_type,expires_at,created_at) values($1,$2,$3,'otp',$4,now())`,
		uuid.NewString(),
		params.UserID,
		otpHash,
		params.ExpiresAt,
	); err != nil {
		return "", err
	}
	if _, err := tx.Exec(
		ctx,
		`insert into email_verifications(id,user_id,token_hash,token_type,expires_at,created_at) values($1,$2,$3,'magic_link',$4,now())`,
		uuid.NewString(),
		params.UserID,
		magicLinkHash,
		params.ExpiresAt,
	); err != nil {
		return "", err
	}

	emailRecordID := uuid.NewString()
	if _, err := tx.Exec(
		ctx,
		`insert into email_records(id,user_id,to_email,template,subject,status,created_at,updated_at) values($1,$2,$3,'verification_email','Verify your email','queued',now(),now())`,
		emailRecordID,
		params.UserID,
		recipientEmail,
	); err != nil {
		return "", err
	}
	return emailRecordID, nil
}
