package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	commonemail "anvilkit-auth-template/modules/common-go/pkg/email"
	"anvilkit-auth-template/services/auth-api/internal/testutil"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStoreCreateVerificationCreatesRowsAndHashesTokens(t *testing.T) {
	db := testutil.MustTestDB(t)
	testutil.TruncateAuthTables(t, db)

	s := &Store{DB: db}
	userID := "store-verify-user"
	email := "verify-user@example.com"
	seedStoreUser(t, db, userID, email)

	expiresAt := time.Now().Add(15 * time.Minute).Round(time.Second)
	params := CreateVerificationParams{
		UserID:     userID,
		Email:      email,
		OTP:        "123456",
		MagicToken: "magic-token-abc",
		ExpiresAt:  expiresAt,
	}
	ctx, cancel := testCtx(t)
	defer cancel()
	res, err := s.CreateVerification(ctx, params)
	if err != nil {
		t.Fatalf("CreateVerification: %v", err)
	}
	if res == nil || res.EmailRecordID == "" {
		t.Fatalf("unexpected create verification result: %+v", res)
	}

	rows, err := db.Query(context.Background(), `
select token_type, token_hash, expires_at
from email_verifications
where user_id=$1`, userID)
	if err != nil {
		t.Fatalf("query email_verifications: %v", err)
	}
	defer rows.Close()

	gotHashes := map[string]string{}
	gotExpires := map[string]time.Time{}
	for rows.Next() {
		var tokenType string
		var tokenHash string
		var gotExpiry time.Time
		if err := rows.Scan(&tokenType, &tokenHash, &gotExpiry); err != nil {
			t.Fatalf("scan email_verifications: %v", err)
		}
		gotHashes[tokenType] = tokenHash
		gotExpires[tokenType] = gotExpiry.Round(time.Second)
	}
	if rows.Err() != nil {
		t.Fatalf("iterate email_verifications: %v", rows.Err())
	}

	if len(gotHashes) != 2 {
		t.Fatalf("verification row count=%d want=2", len(gotHashes))
	}
	if gotHashes["otp"] != commonemail.HashToken(params.OTP) {
		t.Fatalf("otp hash=%q want=%q", gotHashes["otp"], commonemail.HashToken(params.OTP))
	}
	if gotHashes["magic_link"] != commonemail.HashToken(params.MagicToken) {
		t.Fatalf("magic hash=%q want=%q", gotHashes["magic_link"], commonemail.HashToken(params.MagicToken))
	}
	if !gotExpires["otp"].Equal(expiresAt) || !gotExpires["magic_link"].Equal(expiresAt) {
		t.Fatalf("expires_at mismatch otp=%s magic=%s want=%s", gotExpires["otp"], gotExpires["magic_link"], expiresAt)
	}

	var (
		recordEmail   string
		recordStatus  string
		recordSubject string
	)
	err = db.QueryRow(context.Background(), `
select to_email,status,subject
from email_records
where id=$1`, res.EmailRecordID).Scan(&recordEmail, &recordStatus, &recordSubject)
	if err != nil {
		t.Fatalf("query email_records: %v", err)
	}
	if recordEmail != email || recordStatus != "queued" || recordSubject != "Verify your email" {
		t.Fatalf("unexpected email record values: email=%q status=%q subject=%q", recordEmail, recordStatus, recordSubject)
	}
}

func TestStoreCreateVerificationAllowsDuplicateOTPAcrossUsers(t *testing.T) {
	db := testutil.MustTestDB(t)
	testutil.TruncateAuthTables(t, db)

	s := &Store{DB: db}
	userID1 := "store-verify-user-1"
	userID2 := "store-verify-user-2"
	email1 := "verify-user-1@example.com"
	email2 := "verify-user-2@example.com"
	seedStoreUser(t, db, userID1, email1)
	seedStoreUser(t, db, userID2, email2)

	sharedOTP := "123456"
	exp := time.Now().Add(15 * time.Minute).Round(time.Second)
	_, err := s.CreateVerification(context.Background(), CreateVerificationParams{
		UserID:     userID1,
		Email:      email1,
		OTP:        sharedOTP,
		MagicToken: "magic-token-user-1",
		ExpiresAt:  exp,
	})
	if err != nil {
		t.Fatalf("CreateVerification user1: %v", err)
	}
	_, err = s.CreateVerification(context.Background(), CreateVerificationParams{
		UserID:     userID2,
		Email:      email2,
		OTP:        sharedOTP,
		MagicToken: "magic-token-user-2",
		ExpiresAt:  exp,
	})
	if err != nil {
		t.Fatalf("CreateVerification user2 (duplicate OTP hash) should succeed: %v", err)
	}

	otpHash := commonemail.HashToken(sharedOTP)
	var otpCount int
	if err := db.QueryRow(context.Background(), `
select count(1)
from email_verifications
where token_type='otp' and token_hash=$1`, otpHash).Scan(&otpCount); err != nil {
		t.Fatalf("query otp collisions: %v", err)
	}
	if otpCount != 2 {
		t.Fatalf("otp row count=%d want=2", otpCount)
	}
}

func TestStoreVerifyEmailOTPPromotesPendingUser(t *testing.T) {
	db := testutil.MustTestDB(t)
	testutil.TruncateAuthTables(t, db)

	s := &Store{DB: db}
	userID := "store-otp-user"
	emailAddr := "store-otp@example.com"
	_, err := db.Exec(context.Background(), `insert into users(id,email,status,created_at,updated_at) values($1,$2,0,now(),now())`, userID, emailAddr)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	otp := "654321"
	exp := time.Now().Add(15 * time.Minute)
	_, err = db.Exec(context.Background(), `
insert into email_verifications(id,user_id,token_hash,token_type,expires_at,created_at)
values($1,$2,$3,'otp',$4,now())`,
		"verify-otp-row",
		userID,
		commonemail.HashToken(otp),
		exp,
	)
	if err != nil {
		t.Fatalf("insert email verification: %v", err)
	}

	ctx, cancel := testCtx(t)
	defer cancel()
	if err := s.VerifyEmailOTP(ctx, emailAddr, otp, time.Now()); err != nil {
		t.Fatalf("VerifyEmailOTP: %v", err)
	}

	var status int16
	var emailVerifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `select status,email_verified_at from users where id=$1`, userID).Scan(&status, &emailVerifiedAt); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if status != 1 {
		t.Fatalf("status=%d want=1", status)
	}
	if emailVerifiedAt == nil {
		t.Fatal("email_verified_at should be set")
	}

	var verifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `select verified_at from email_verifications where id='verify-otp-row'`).Scan(&verifiedAt); err != nil {
		t.Fatalf("query verification row: %v", err)
	}
	if verifiedAt == nil {
		t.Fatal("email_verifications.verified_at should be set")
	}
}

func TestStoreVerifyMagicLinkTokenPromotesPendingUser(t *testing.T) {
	db := testutil.MustTestDB(t)
	testutil.TruncateAuthTables(t, db)

	s := &Store{DB: db}
	userID := "store-magic-user"
	emailAddr := "store-magic@example.com"
	_, err := db.Exec(context.Background(), `insert into users(id,email,status,created_at,updated_at) values($1,$2,0,now(),now())`, userID, emailAddr)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	magicToken := "magic-link-token-123"
	exp := time.Now().Add(15 * time.Minute)
	_, err = db.Exec(context.Background(), `
insert into email_verifications(id,user_id,token_hash,token_type,expires_at,created_at)
values($1,$2,$3,'magic_link',$4,now())`,
		"verify-magic-row",
		userID,
		commonemail.HashToken(magicToken),
		exp,
	)
	if err != nil {
		t.Fatalf("insert magic verification: %v", err)
	}

	ctx, cancel := testCtx(t)
	defer cancel()
	if err := s.VerifyMagicLinkToken(ctx, magicToken, time.Now()); err != nil {
		t.Fatalf("VerifyMagicLinkToken: %v", err)
	}

	var status int16
	var emailVerifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `select status,email_verified_at from users where id=$1`, userID).Scan(&status, &emailVerifiedAt); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if status != 1 {
		t.Fatalf("status=%d want=1", status)
	}
	if emailVerifiedAt == nil {
		t.Fatal("email_verified_at should be set")
	}

	var verifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `select verified_at from email_verifications where id='verify-magic-row'`).Scan(&verifiedAt); err != nil {
		t.Fatalf("query magic verification row: %v", err)
	}
	if verifiedAt == nil {
		t.Fatal("magic email_verifications.verified_at should be set")
	}
}

func TestStoreRegisterWithVerificationAndCleanupPendingRegistration(t *testing.T) {
	db := testutil.MustTestDB(t)
	testutil.TruncateAuthTables(t, db)

	s := &Store{DB: db}
	ctx, cancel := testCtx(t)
	defer cancel()
	res, err := s.RegisterWithVerification(
		ctx,
		"atomic-register@example.com",
		"Passw0rd!",
		4,
		"123456",
		"magic-atomic-token",
		time.Now().Add(15*time.Minute),
	)
	if err != nil {
		t.Fatalf("RegisterWithVerification: %v", err)
	}
	if res == nil || res.User.ID == "" || res.EmailRecordID == "" {
		t.Fatalf("unexpected register-with-verification result: %+v", res)
	}

	var usersCount int
	if err := db.QueryRow(context.Background(), `select count(1) from users where id=$1 and email=$2 and status=0`, res.User.ID, "atomic-register@example.com").Scan(&usersCount); err != nil {
		t.Fatalf("query users: %v", err)
	}
	if usersCount != 1 {
		t.Fatalf("users count=%d want=1", usersCount)
	}
	var verificationsCount int
	if err := db.QueryRow(context.Background(), `select count(1) from email_verifications where user_id=$1`, res.User.ID).Scan(&verificationsCount); err != nil {
		t.Fatalf("query email_verifications: %v", err)
	}
	if verificationsCount != 2 {
		t.Fatalf("email_verifications count=%d want=2", verificationsCount)
	}
	var recordsCount int
	if err := db.QueryRow(context.Background(), `select count(1) from email_records where user_id=$1`, res.User.ID).Scan(&recordsCount); err != nil {
		t.Fatalf("query email_records: %v", err)
	}
	if recordsCount != 1 {
		t.Fatalf("email_records count=%d want=1", recordsCount)
	}

	ctxCleanup, cancelCleanup := testCtx(t)
	defer cancelCleanup()
	if err := s.CleanupPendingRegistration(ctxCleanup, res.User.ID); err != nil {
		t.Fatalf("CleanupPendingRegistration: %v", err)
	}

	if err := db.QueryRow(context.Background(), `select count(1) from users where id=$1`, res.User.ID).Scan(&usersCount); err != nil {
		t.Fatalf("query users after cleanup: %v", err)
	}
	if usersCount != 0 {
		t.Fatalf("users count after cleanup=%d want=0", usersCount)
	}
	if err := db.QueryRow(context.Background(), `select count(1) from email_verifications where user_id=$1`, res.User.ID).Scan(&verificationsCount); err != nil {
		t.Fatalf("query email_verifications after cleanup: %v", err)
	}
	if verificationsCount != 0 {
		t.Fatalf("email_verifications after cleanup=%d want=0", verificationsCount)
	}
	if err := db.QueryRow(context.Background(), `select count(1) from email_records where id=$1`, res.EmailRecordID).Scan(&recordsCount); err != nil {
		t.Fatalf("query email_records after cleanup: %v", err)
	}
	if recordsCount != 0 {
		t.Fatalf("email_records after cleanup=%d want=0", recordsCount)
	}
}

func TestStoreRotateRefreshTokenRevokesOldAndCreatesNew(t *testing.T) {
	db := testutil.MustTestDB(t)
	testutil.TruncateAuthTables(t, db)

	s := &Store{DB: db}
	uid := "store-rotate-user"
	seedStoreUser(t, db, uid, "store-rotate@example.com")

	oldToken := "store-old-token"
	oldHash := sha256.Sum256([]byte(oldToken))
	oldHashHex := hex.EncodeToString(oldHash[:])
	ctx, cancel := testCtx(t)
	defer cancel()
	_, err := db.Exec(ctx, `
insert into refresh_sessions(id,user_id,token_hash,expires_at,created_at)
values($1,$2,$3,$4,now())`, "store-session-old", uid, oldHashHex, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("insert old refresh session: %v", err)
	}

	newToken := "store-new-token"
	ctxRotate, cancelRotate := testCtx(t)
	defer cancelRotate()
	gotUID, _, err := s.RotateRefreshToken(ctxRotate, oldToken, newToken, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("RotateRefreshToken error: %v", err)
	}
	if gotUID != uid {
		t.Fatalf("uid=%q want=%q", gotUID, uid)
	}

	var revokedAt *time.Time
	var replacedBy *string
	ctxQuery, cancelQuery := testCtx(t)
	defer cancelQuery()
	err = db.QueryRow(ctxQuery, `select revoked_at, replaced_by from refresh_sessions where token_hash=$1`, oldHashHex).Scan(&revokedAt, &replacedBy)
	if err != nil {
		t.Fatalf("query old session: %v", err)
	}
	if revokedAt == nil || replacedBy == nil || *replacedBy == "" {
		t.Fatalf("old session should be revoked and linked: revokedAt=%v replacedBy=%v", revokedAt, replacedBy)
	}
}

func TestStoreRevokeRefreshTokenMakesRotationFail(t *testing.T) {
	db := testutil.MustTestDB(t)
	testutil.TruncateAuthTables(t, db)

	s := &Store{DB: db}
	uid := "store-revoke-user"
	seedStoreUser(t, db, uid, "store-revoke@example.com")
	refreshToken := "store-revoke-token"
	ctxSave, cancelSave := testCtx(t)
	defer cancelSave()
	if err := s.SaveRefreshSession(ctxSave, refreshToken, uid, time.Now().Add(time.Hour), "", ""); err != nil {
		t.Fatalf("SaveRefreshSession: %v", err)
	}

	ctxRevoke, cancelRevoke := testCtx(t)
	defer cancelRevoke()
	if err := s.RevokeRefreshToken(ctxRevoke, refreshToken); err != nil {
		t.Fatalf("RevokeRefreshToken: %v", err)
	}

	ctxRotate, cancelRotate := testCtx(t)
	defer cancelRotate()
	_, _, err := s.RotateRefreshToken(ctxRotate, refreshToken, "another-token", time.Now().Add(time.Hour))
	if !errors.Is(err, ErrRefreshSessionRevoked) {
		t.Fatalf("RotateRefreshToken err=%v, want %v", err, ErrRefreshSessionRevoked)
	}
}

func seedStoreUser(t *testing.T, db *pgxpool.Pool, userID, email string) {
	t.Helper()
	ctx, cancel := testCtx(t)
	defer cancel()
	_, err := db.Exec(ctx, `insert into users(id,email,status,created_at,updated_at) values($1,$2,1,now(),now())`, userID, email)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func testCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}
