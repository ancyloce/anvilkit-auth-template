package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/testutil"
)

func TestRefreshSuccessRotation(t *testing.T) {
	db := newTestDB(t)
	testutil.TruncateAuthTables(t, db)

	uid := "refresh-success-user"
	seedRefreshUser(t, db, uid, "refresh-success@example.com")
	oldToken := "old-refresh-success-token"
	oldHash := sha256.Sum256([]byte(oldToken))
	oldID := "refresh-old-session"
	_, err := db.Exec(context.Background(), `
insert into refresh_sessions(id,user_id,token_hash,expires_at,created_at)
values($1,$2,$3,$4,now())`, oldID, uid, hex.EncodeToString(oldHash[:]), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("insert old refresh session: %v", err)
	}

	r := newRefreshRouter(t, db)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{"refresh_token": oldToken})
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusOK, res.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			AccessToken      string `json:"access_token"`
			RefreshToken     string `json:"refresh_token"`
			ExpiresIn        int    `json:"expires_in"`
			RefreshExpiresIn int    `json:"refresh_expires_in"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != 0 {
		t.Fatalf("unexpected envelope code=%d", body.Code)
	}
	if body.Data.AccessToken == "" || body.Data.RefreshToken == "" {
		t.Fatalf("unexpected empty tokens: %+v", body.Data)
	}
	if body.Data.RefreshToken == oldToken {
		t.Fatal("refresh token should rotate")
	}
	if body.Data.ExpiresIn != 900 || body.Data.RefreshExpiresIn != 604800 {
		t.Fatalf("unexpected ttl values: access=%d refresh=%d", body.Data.ExpiresIn, body.Data.RefreshExpiresIn)
	}

	var revokedAt *time.Time
	var replacedBy *string
	err = db.QueryRow(context.Background(), `select revoked_at, replaced_by from refresh_sessions where id=$1`, oldID).Scan(&revokedAt, &replacedBy)
	if err != nil {
		t.Fatalf("query old session: %v", err)
	}
	if revokedAt == nil {
		t.Fatal("old session should be revoked")
	}
	if replacedBy == nil || *replacedBy == "" {
		t.Fatal("old session should have replaced_by")
	}

	newHash := sha256.Sum256([]byte(body.Data.RefreshToken))
	var persistedHash, newSessionID string
	err = db.QueryRow(context.Background(), `select id, token_hash from refresh_sessions where id=$1`, *replacedBy).Scan(&newSessionID, &persistedHash)
	if err != nil {
		t.Fatalf("query new session by replaced_by: %v", err)
	}
	if newSessionID == oldID {
		t.Fatal("new session id must differ from old session id")
	}
	if persistedHash != hex.EncodeToString(newHash[:]) {
		t.Fatalf("new session token hash mismatch")
	}
}

func TestRefreshReplayFailsWithSessionRevoked(t *testing.T) {
	db := newTestDB(t)
	testutil.TruncateAuthTables(t, db)

	uid := "refresh-replay-user"
	seedRefreshUser(t, db, uid, "refresh-replay@example.com")
	oldToken := "old-refresh-replay-token"
	oldHash := sha256.Sum256([]byte(oldToken))
	_, err := db.Exec(context.Background(), `
insert into refresh_sessions(id,user_id,token_hash,expires_at,created_at)
values($1,$2,$3,$4,now())`, "refresh-replay-old", uid, hex.EncodeToString(oldHash[:]), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("insert old refresh session: %v", err)
	}

	r := newRefreshRouter(t, db)
	first := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{"refresh_token": oldToken})
	if first.Code != http.StatusOK {
		t.Fatalf("first refresh status=%d body=%s", first.Code, first.Body.String())
	}

	second := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{"refresh_token": oldToken})
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("second refresh status=%d want=%d body=%s", second.Code, http.StatusUnauthorized, second.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, second, &body)
	if body.Code != errcode.Unauthorized {
		t.Fatalf("code=%d, want unauthorized", body.Code)
	}
	if body.Data.Reason != "session_revoked" {
		t.Fatalf("reason=%q, want session_revoked", body.Data.Reason)
	}
}

func TestRefreshExpiredFails(t *testing.T) {
	db := newTestDB(t)
	testutil.TruncateAuthTables(t, db)

	uid := "refresh-expired-user"
	seedRefreshUser(t, db, uid, "refresh-expired@example.com")
	token := "refresh-expired-token"
	h := sha256.Sum256([]byte(token))
	_, err := db.Exec(context.Background(), `
insert into refresh_sessions(id,user_id,token_hash,expires_at,created_at)
values($1,$2,$3,$4,now())`, "refresh-expired-old", uid, hex.EncodeToString(h[:]), time.Now().Add(-1*time.Minute))
	if err != nil {
		t.Fatalf("insert expired refresh session: %v", err)
	}

	r := newRefreshRouter(t, db)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{"refresh_token": token})
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusUnauthorized, res.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Unauthorized {
		t.Fatalf("code=%d, want unauthorized", body.Code)
	}
	if body.Data.Reason != "refresh_expired" {
		t.Fatalf("reason=%q, want refresh_expired", body.Data.Reason)
	}
}

func TestRefreshRevokedFails(t *testing.T) {
	db := newTestDB(t)
	testutil.TruncateAuthTables(t, db)

	uid := "refresh-revoked-user"
	seedRefreshUser(t, db, uid, "refresh-revoked@example.com")
	token := "refresh-revoked-token"
	h := sha256.Sum256([]byte(token))
	_, err := db.Exec(context.Background(), `
insert into refresh_sessions(id,user_id,token_hash,expires_at,revoked_at,created_at)
values($1,$2,$3,$4,now(),now())`, "refresh-revoked-old", uid, hex.EncodeToString(h[:]), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("insert revoked refresh session: %v", err)
	}

	r := newRefreshRouter(t, db)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{"refresh_token": token})
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusUnauthorized, res.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Unauthorized {
		t.Fatalf("code=%d, want unauthorized", body.Code)
	}
	if body.Data.Reason != "session_revoked" {
		t.Fatalf("reason=%q, want session_revoked", body.Data.Reason)
	}
}

func newRefreshRouter(t *testing.T, db *pgxpool.Pool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := newTestAuthHandler(t, db, nil)
	r.POST("/v1/auth/refresh", ginmid.Wrap(h.Refresh))
	return r
}

func seedRefreshUser(t *testing.T, db *pgxpool.Pool, userID, email string) {
	t.Helper()
	_, err := db.Exec(context.Background(), `insert into users(id,email,status,created_at,updated_at) values($1,$2,1,now(),now())`, userID, email)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
}
