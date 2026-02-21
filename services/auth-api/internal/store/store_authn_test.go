package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"anvilkit-auth-template/services/auth-api/internal/testutil"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
