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

	ajwt "anvilkit-auth-template/modules/common-go/pkg/auth/jwt"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/store"
)

func TestLogoutRevokesSessionAndRefreshFails(t *testing.T) {
	db := newTestDB(t)
	clearAuthTables(t, db)

	uid := "logout-user"
	seedRefreshUser(t, db, uid, "logout@example.com")
	refreshToken := "logout-refresh-token"
	h := sha256.Sum256([]byte(refreshToken))
	_, err := db.Exec(context.Background(), `
insert into refresh_sessions(id,user_id,token_hash,expires_at,created_at)
values($1,$2,$3,$4,now())`, "logout-session", uid, hex.EncodeToString(h[:]), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("insert refresh session: %v", err)
	}

	r := newLogoutRouter(db)
	logoutRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/logout", map[string]string{"refresh_token": refreshToken})
	if logoutRes.Code != http.StatusOK {
		t.Fatalf("logout status=%d want=%d body=%s", logoutRes.Code, http.StatusOK, logoutRes.Body.String())
	}
	var logoutBody struct {
		Code int `json:"code"`
		Data struct {
			OK bool `json:"ok"`
		} `json:"data"`
	}
	decodeResponse(t, logoutRes, &logoutBody)
	if logoutBody.Code != 0 || !logoutBody.Data.OK {
		t.Fatalf("unexpected logout body: %+v", logoutBody)
	}

	refreshRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{"refresh_token": refreshToken})
	if refreshRes.Code != http.StatusUnauthorized {
		t.Fatalf("refresh status=%d want=%d body=%s", refreshRes.Code, http.StatusUnauthorized, refreshRes.Body.String())
	}
	var refreshBody struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, refreshRes, &refreshBody)
	if refreshBody.Code != errcode.Unauthorized {
		t.Fatalf("code=%d, want unauthorized", refreshBody.Code)
	}
	if refreshBody.Data.Reason != "session_revoked" {
		t.Fatalf("reason=%q, want session_revoked", refreshBody.Data.Reason)
	}
}

func TestLogoutAllRevokesAllSessions(t *testing.T) {
	db := newTestDB(t)
	clearAuthTables(t, db)

	uid := "logout-all-user"
	seedRefreshUser(t, db, uid, "logout-all@example.com")
	seedRefreshSession(t, db, "logout-all-session-1", uid, "logout-all-token-1")
	seedRefreshSession(t, db, "logout-all-session-2", uid, "logout-all-token-2")

	otherUID := "logout-all-other-user"
	seedRefreshUser(t, db, otherUID, "logout-all-other@example.com")
	seedRefreshSession(t, db, "logout-all-session-other", otherUID, "logout-all-token-other")

	r := newLogoutRouter(db)
	accessToken, err := ajwt.Sign("test-secret", "anvilkit-auth", "anvilkit-clients", uid, "", "access", 15*time.Minute)
	if err != nil {
		t.Fatalf("sign access token: %v", err)
	}
	logoutAllRes := performAuthedJSONRequest(t, r, http.MethodPost, "/v1/auth/logout_all", accessToken, map[string]any{})
	if logoutAllRes.Code != http.StatusOK {
		t.Fatalf("logout_all status=%d want=%d body=%s", logoutAllRes.Code, http.StatusOK, logoutAllRes.Body.String())
	}
	var logoutAllBody struct {
		Code int `json:"code"`
		Data struct {
			OK           bool  `json:"ok"`
			RevokedCount int64 `json:"revoked_count"`
		} `json:"data"`
	}
	decodeResponse(t, logoutAllRes, &logoutAllBody)
	if logoutAllBody.Code != 0 || !logoutAllBody.Data.OK {
		t.Fatalf("unexpected logout_all body: %+v", logoutAllBody)
	}
	if logoutAllBody.Data.RevokedCount != 2 {
		t.Fatalf("revoked_count=%d, want 2", logoutAllBody.Data.RevokedCount)
	}

	var activeCount int
	err = db.QueryRow(context.Background(), `select count(1) from refresh_sessions where user_id=$1 and revoked_at is null`, uid).Scan(&activeCount)
	if err != nil {
		t.Fatalf("query active sessions for user: %v", err)
	}
	if activeCount != 0 {
		t.Fatalf("active sessions=%d, want 0", activeCount)
	}

	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/refresh", map[string]string{"refresh_token": "logout-all-token-1"})
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("refresh status=%d want=%d body=%s", res.Code, http.StatusUnauthorized, res.Body.String())
	}

	err = db.QueryRow(context.Background(), `select count(1) from refresh_sessions where user_id=$1 and revoked_at is null`, otherUID).Scan(&activeCount)
	if err != nil {
		t.Fatalf("query active sessions for other user: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("other user active sessions=%d, want 1", activeCount)
	}
}

func newLogoutRouter(db *pgxpool.Pool) *gin.Engine {
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
	r.POST("/v1/auth/logout", ginmid.Wrap(h.Logout))
	r.POST("/v1/auth/logout_all", ginmid.AuthN(h.JWTSecret, h.JWTIssuer, h.JWTAudience), ginmid.Wrap(h.LogoutAll))
	r.POST("/v1/auth/refresh", ginmid.Wrap(h.Refresh))
	return r
}

func seedRefreshSession(t *testing.T, db *pgxpool.Pool, sessionID, userID, token string) {
	t.Helper()
	h := sha256.Sum256([]byte(token))
	_, err := db.Exec(context.Background(), `
insert into refresh_sessions(id,user_id,token_hash,expires_at,created_at)
values($1,$2,$3,$4,now())`, sessionID, userID, hex.EncodeToString(h[:]), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("insert refresh session: %v", err)
	}
}
