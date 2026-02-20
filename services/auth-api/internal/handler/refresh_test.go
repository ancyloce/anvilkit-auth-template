package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/store"
)

func newRefreshRouter(db *pgxpool.Pool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := &Handler{
		Store:       &store.Store{DB: db},
		JWTIssuer:   "anvilkit-auth",
		JWTAudience: "anvilkit-clients",
		JWTSecret:   "test-secret",
		AccessTTL:   15 * time.Minute,
		RefreshTTL:  168 * time.Hour,
	}
	r.POST("/v1/auth/refresh", ginmid.Wrap(h.Refresh))
	return r
}

// insertRefreshSession inserts a refresh session directly into DB for testing.
func insertRefreshSession(t *testing.T, db *pgxpool.Pool, rawToken, userID string, expiresAt time.Time, revokedAt *time.Time) {
	t.Helper()
	h := sha256.Sum256([]byte(rawToken))
	id := uuid.NewString()
	_, err := db.Exec(context.Background(),
		`insert into refresh_sessions(id,user_id,token_hash,expires_at,revoked_at,created_at)
		 values($1,$2,$3,$4,$5,now())`,
		id, userID, hex.EncodeToString(h[:]), expiresAt, revokedAt,
	)
	if err != nil {
		t.Fatalf("insertRefreshSession: %v", err)
	}
}

// insertUser inserts a minimal user row for testing.
func insertTestUser(t *testing.T, db *pgxpool.Pool) string {
	t.Helper()
	uid := uuid.NewString()
	_, err := db.Exec(context.Background(),
		`insert into users(id,email,status,created_at,updated_at) values($1,$2,1,now(),now())`,
		uid, uid+"@example.com",
	)
	if err != nil {
		t.Fatalf("insertTestUser: %v", err)
	}
	return uid
}

// TestRefreshSuccess tests normal token rotation.
func TestRefreshSuccess(t *testing.T) {
	db := newTestDB(t)
	clearAuthTables(t, db)

	uid := insertTestUser(t, db)
	rawToken := "good-refresh-token-" + uuid.NewString()
	insertRefreshSession(t, db, rawToken, uid, time.Now().Add(24*time.Hour), nil)

	r := newRefreshRouter(db)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{
		"refresh_token": rawToken,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", res.Code, res.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			AccessToken      string `json:"access_token"`
			ExpiresIn        int    `json:"expires_in"`
			RefreshToken     string `json:"refresh_token"`
			RefreshExpiresIn int    `json:"refresh_expires_in"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != 0 {
		t.Fatalf("envelope code=%d want 0", body.Code)
	}
	if body.Data.AccessToken == "" {
		t.Fatal("access_token must not be empty")
	}
	if body.Data.RefreshToken == "" {
		t.Fatal("refresh_token must not be empty")
	}
	if body.Data.ExpiresIn != 900 {
		t.Fatalf("expires_in=%d want 900", body.Data.ExpiresIn)
	}
	if body.Data.RefreshExpiresIn != 604800 {
		t.Fatalf("refresh_expires_in=%d want 604800", body.Data.RefreshExpiresIn)
	}

	// Old session must be revoked and point to the new session.
	oldHash := sha256.Sum256([]byte(rawToken))
	var revokedAt *time.Time
	var replacedBy *string
	err := db.QueryRow(context.Background(),
		`select revoked_at, replaced_by from refresh_sessions where token_hash=$1`,
		hex.EncodeToString(oldHash[:]),
	).Scan(&revokedAt, &replacedBy)
	if err != nil {
		t.Fatalf("query old session: %v", err)
	}
	if revokedAt == nil {
		t.Fatal("old session revoked_at must be set")
	}
	if replacedBy == nil || *replacedBy == "" {
		t.Fatal("old session replaced_by must be set")
	}

	// New session must exist with the new token hash.
	newHash := sha256.Sum256([]byte(body.Data.RefreshToken))
	var newCount int
	err = db.QueryRow(context.Background(),
		`select count(1) from refresh_sessions where token_hash=$1 and revoked_at is null`,
		hex.EncodeToString(newHash[:]),
	).Scan(&newCount)
	if err != nil {
		t.Fatalf("query new session: %v", err)
	}
	if newCount != 1 {
		t.Fatalf("new session count=%d want 1", newCount)
	}
}

// TestRefreshReplayFails tests that replaying the same token fails.
func TestRefreshReplayFails(t *testing.T) {
	db := newTestDB(t)
	clearAuthTables(t, db)

	uid := insertTestUser(t, db)
	rawToken := "replay-token-" + uuid.NewString()
	insertRefreshSession(t, db, rawToken, uid, time.Now().Add(24*time.Hour), nil)

	r := newRefreshRouter(db)

	// First refresh must succeed.
	res1 := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{
		"refresh_token": rawToken,
	})
	if res1.Code != http.StatusOK {
		t.Fatalf("first refresh: status=%d want 200; body=%s", res1.Code, res1.Body.String())
	}

	// Second refresh with the same old token must fail.
	res2 := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{
		"refresh_token": rawToken,
	})
	if res2.Code != http.StatusUnauthorized {
		t.Fatalf("replay: status=%d want 401; body=%s", res2.Code, res2.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, res2, &body)
	if body.Code != errcode.Unauthorized {
		t.Fatalf("replay: code=%d want %d", body.Code, errcode.Unauthorized)
	}
	if body.Data.Reason != "session_revoked" {
		t.Fatalf("replay: reason=%q want session_revoked", body.Data.Reason)
	}
}

// TestRefreshExpiredFails tests that an expired refresh token is rejected.
func TestRefreshExpiredFails(t *testing.T) {
	db := newTestDB(t)
	clearAuthTables(t, db)

	uid := insertTestUser(t, db)
	rawToken := "expired-token-" + uuid.NewString()
	// expires_at in the past
	insertRefreshSession(t, db, rawToken, uid, time.Now().Add(-time.Hour), nil)

	r := newRefreshRouter(db)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{
		"refresh_token": rawToken,
	})
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expired: status=%d want 401; body=%s", res.Code, res.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Unauthorized {
		t.Fatalf("expired: code=%d want %d", body.Code, errcode.Unauthorized)
	}
	if body.Data.Reason != "refresh_expired" {
		t.Fatalf("expired: reason=%q want refresh_expired", body.Data.Reason)
	}
}

// TestRefreshRevokedFails tests that a pre-revoked refresh token is rejected.
func TestRefreshRevokedFails(t *testing.T) {
	db := newTestDB(t)
	clearAuthTables(t, db)

	uid := insertTestUser(t, db)
	rawToken := "revoked-token-" + uuid.NewString()
	now := time.Now()
	insertRefreshSession(t, db, rawToken, uid, now.Add(24*time.Hour), &now)

	r := newRefreshRouter(db)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{
		"refresh_token": rawToken,
	})
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("revoked: status=%d want 401; body=%s", res.Code, res.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Unauthorized {
		t.Fatalf("revoked: code=%d want %d", body.Code, errcode.Unauthorized)
	}
	if body.Data.Reason != "session_revoked" {
		t.Fatalf("revoked: reason=%q want session_revoked", body.Data.Reason)
	}
}
