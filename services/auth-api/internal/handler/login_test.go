package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/store"
)

func newTestRedis(t *testing.T) *goredis.Client {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("TEST_REDIS_ADDR"))
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	}
	if addr == "" {
		addr = "localhost:6379"
	}
	rdb := goredis.NewClient(&goredis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func clearLoginFailKeys(t *testing.T, rdb *goredis.Client) {
	t.Helper()
	keys, err := rdb.Keys(context.Background(), "login_fail:*").Result()
	if err != nil || len(keys) == 0 {
		return
	}
	if err := rdb.Del(context.Background(), keys...).Err(); err != nil {
		t.Logf("clearLoginFailKeys: %v", err)
	}
}

func newLoginRouter(db *pgxpool.Pool, rdb *goredis.Client, failLimit int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := &Handler{
		Store:           &store.Store{DB: db},
		JWTSecret:       "test-secret",
		JWTIssuer:       "test-issuer",
		JWTAudience:     "test-audience",
		AccessTTL:       15 * time.Minute,
		RefreshTTL:      7 * 24 * time.Hour,
		PasswordMinLen:  6,
		BcryptCost:      4,
		Redis:           rdb,
		LoginFailLimit:  failLimit,
		LoginFailWindow: 10 * time.Minute,
	}
	r.POST("/v1/auth/register", ginmid.Wrap(h.Register))
	r.POST("/v1/auth/login", ginmid.Wrap(h.LoginSession))
	return r
}

// performLoginRequest creates a test request for /v1/auth/login with an
// explicit RemoteAddr so the fail-counter key is deterministic.
func performLoginRequest(t *testing.T, r *gin.Engine, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestLoginSuccess registers a user then logs in and validates the full response.
func TestLoginSuccess(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	clearAuthTables(t, db)
	clearLoginFailKeys(t, rdb)

	r := newLoginRouter(db, rdb, 5)

	performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "login_ok@example.com",
		"password": "Passw0rd!",
	})

	res := performLoginRequest(t, r, "login_ok@example.com", "Passw0rd!")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", res.Code, res.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			AccessToken      string `json:"access_token"`
			ExpiresIn        int    `json:"expires_in"`
			RefreshToken     string `json:"refresh_token"`
			RefreshExpiresIn int    `json:"refresh_expires_in"`
			User             struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"user"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.OK {
		t.Fatalf("envelope code = %d, want 0; body = %s", body.Code, res.Body.String())
	}
	if body.Data.AccessToken == "" {
		t.Fatal("access_token should not be empty")
	}
	if body.Data.RefreshToken == "" {
		t.Fatal("refresh_token should not be empty")
	}
	if body.Data.ExpiresIn <= 0 {
		t.Fatalf("expires_in = %d, want > 0", body.Data.ExpiresIn)
	}
	if body.Data.RefreshExpiresIn <= 0 {
		t.Fatalf("refresh_expires_in = %d, want > 0", body.Data.RefreshExpiresIn)
	}
	if body.Data.User.ID == "" {
		t.Fatal("user.id should not be empty")
	}
	if body.Data.User.Email != "login_ok@example.com" {
		t.Fatalf("user.email = %q, want %q", body.Data.User.Email, "login_ok@example.com")
	}

	// DB must have exactly 1 refresh_session row.
	var count int
	if err := db.QueryRow(context.Background(),
		"select count(1) from refresh_sessions").Scan(&count); err != nil {
		t.Fatalf("query refresh_sessions: %v", err)
	}
	if count != 1 {
		t.Fatalf("refresh_sessions count = %d, want 1", count)
	}

	// Stored token_hash must differ from the raw token returned to the client.
	var storedHash string
	if err := db.QueryRow(context.Background(),
		"select token_hash from refresh_sessions limit 1").Scan(&storedHash); err != nil {
		t.Fatalf("query token_hash: %v", err)
	}
	if storedHash == body.Data.RefreshToken {
		t.Fatal("token_hash in DB must not equal the raw refresh_token returned to the client")
	}
}

// TestLoginWrongPassword checks that an incorrect password returns 401 and
// increments the Redis fail counter.
func TestLoginWrongPassword(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	clearAuthTables(t, db)
	clearLoginFailKeys(t, rdb)

	r := newLoginRouter(db, rdb, 5)

	performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "fail_pwd@example.com",
		"password": "Passw0rd!",
	})

	res := performLoginRequest(t, r, "fail_pwd@example.com", "WrongPassword")
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", res.Code, res.Body.String())
	}
	var body struct{ Code int `json:"code"` }
	decodeResponse(t, res, &body)
	if body.Code != errcode.Unauthorized {
		t.Fatalf("code = %d, want %d", body.Code, errcode.Unauthorized)
	}

	// Redis fail counter must be >= 1.
	keys, _ := rdb.Keys(context.Background(), "login_fail:*").Result()
	if len(keys) == 0 {
		t.Fatal("expected at least one login_fail key in Redis after wrong-password attempt")
	}
	n, _ := rdb.Get(context.Background(), keys[0]).Int()
	if n < 1 {
		t.Fatalf("fail counter = %d, want >= 1", n)
	}
}

// TestLoginUserNotFound checks that a non-existent email returns 401 and
// increments the fail counter (same behaviour as wrong password to avoid
// user-enumeration).
func TestLoginUserNotFound(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	clearAuthTables(t, db)
	clearLoginFailKeys(t, rdb)

	r := newLoginRouter(db, rdb, 5)

	res := performLoginRequest(t, r, "nobody@example.com", "SomePassword")
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", res.Code, res.Body.String())
	}
	var body struct{ Code int `json:"code"` }
	decodeResponse(t, res, &body)
	if body.Code != errcode.Unauthorized {
		t.Fatalf("code = %d, want %d", body.Code, errcode.Unauthorized)
	}

	keys, _ := rdb.Keys(context.Background(), "login_fail:*").Result()
	if len(keys) == 0 {
		t.Fatal("expected at least one login_fail key in Redis after user-not-found attempt")
	}
}

// TestLoginRateLimit makes repeated failed login attempts until the threshold
// is reached and then verifies the next request returns 429.
func TestLoginRateLimit(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	clearAuthTables(t, db)
	clearLoginFailKeys(t, rdb)

	const limit = 3
	r := newLoginRouter(db, rdb, limit)

	// Make `limit` failed attempts with wrong password.
	for i := 0; i < limit; i++ {
		res := performLoginRequest(t, r, "rl@example.com", "WrongPassword")
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, res.Code)
		}
	}

	// The next attempt must be rate-limited.
	res := performLoginRequest(t, r, "rl@example.com", "WrongPassword")
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body = %s", res.Code, res.Body.String())
	}
	var body struct{ Code int `json:"code"` }
	decodeResponse(t, res, &body)
	if body.Code != errcode.RateLimited {
		t.Fatalf("code = %d, want %d", body.Code, errcode.RateLimited)
	}
}
