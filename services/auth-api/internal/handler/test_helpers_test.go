package handler

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"anvilkit-auth-template/services/auth-api/internal/store"
	"anvilkit-auth-template/services/auth-api/internal/testutil"
)

func newTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testutil.MustTestDB(t)
}

func newTestRedis(t *testing.T) *goredis.Client {
	t.Helper()
	return testutil.MustTestRedis(t)
}

func mustJWTSecret(t *testing.T) string {
	t.Helper()
	return testutil.MustJWTSecret(t)
}

func performJSONRequest(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func performAuthedJSONRequest(t *testing.T, r *gin.Engine, method, path, accessToken string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeResponse(t *testing.T, res *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(res.Body.Bytes(), out); err != nil {
		t.Fatalf("json unmarshal: %v body=%s", err, res.Body.String())
	}
}

func newTestAuthHandler(t *testing.T, db *pgxpool.Pool, rdb *goredis.Client) *Handler {
	t.Helper()
	return &Handler{
		Store:           &store.Store{DB: db},
		Redis:           rdb,
		JWTIssuer:       "anvilkit-auth",
		JWTAudience:     "anvilkit-clients",
		JWTSecret:       mustJWTSecret(t),
		AccessTTL:       15 * time.Minute,
		RefreshTTL:      168 * time.Hour,
		PasswordMinLen:  8,
		BcryptCost:      4,
		LoginFailLimit:  5,
		LoginFailWindow: 10 * time.Minute,
	}
}
