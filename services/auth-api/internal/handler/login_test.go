package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/auth/crypto"
	"anvilkit-auth-template/services/auth-api/internal/store"
)

func TestLoginSuccess(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	clearAuthTables(t, db)
	clearLoginFailKeys(t, rdb)

	seedLoginUser(t, db, "login-ok@example.com", "Passw0rd!", 1)
	r := newLoginRouter(db, rdb)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "login-ok@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", res.Code, http.StatusOK, res.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			AccessToken      string `json:"access_token"`
			RefreshToken     string `json:"refresh_token"`
			ExpiresIn        int    `json:"expires_in"`
			RefreshExpiresIn int    `json:"refresh_expires_in"`
			User             struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"user"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != 0 || body.Data.AccessToken == "" || body.Data.RefreshToken == "" {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.Data.ExpiresIn != 900 || body.Data.RefreshExpiresIn != 604800 {
		t.Fatalf("unexpected ttl values: access=%d refresh=%d", body.Data.ExpiresIn, body.Data.RefreshExpiresIn)
	}

	refreshHash := sha256.Sum256([]byte(body.Data.RefreshToken))
	var count int
	var tokenHash string
	err := db.QueryRow(context.Background(), `select count(1), max(token_hash) from refresh_sessions where user_id=$1`, body.Data.User.ID).Scan(&count, &tokenHash)
	if err != nil {
		t.Fatalf("query refresh_sessions: %v", err)
	}
	if count != 1 {
		t.Fatalf("refresh_sessions count=%d, want 1", count)
	}
	if tokenHash != hex.EncodeToString(refreshHash[:]) {
		t.Fatalf("token hash mismatch")
	}
	if tokenHash == body.Data.RefreshToken {
		t.Fatalf("db token_hash should not equal raw refresh_token")
	}

	// verify that the login failure counter is cleared after successful login
	key := "login_fail:192.0.2.1:login-ok@example.com"
	failCount, err := rdb.Get(context.Background(), key).Int()
	if err != nil && err != goredis.Nil {
		t.Fatalf("redis get login fail count: %v", err)
	}
	if err == nil && failCount != 0 {
		t.Fatalf("login fail count=%d, want 0 or key to be deleted", failCount)
	}
}

func TestLoginWrongPasswordUnauthorizedAndIncr(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	clearAuthTables(t, db)
	clearLoginFailKeys(t, rdb)

	seedLoginUser(t, db, "wrong-pass@example.com", "Passw0rd!", 1)
	r := newLoginRouter(db, rdb)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "wrong-pass@example.com",
		"password": "bad-pass",
	})
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
	var body struct {
		Code int `json:"code"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Unauthorized {
		t.Fatalf("code=%d, want unauthorized", body.Code)
	}
	key := "login_fail:192.0.2.1:wrong-pass@example.com"
	count, err := rdb.Get(context.Background(), key).Int()
	if err != nil {
		t.Fatalf("redis get fail count: %v", err)
	}
	if count != 1 {
		t.Fatalf("fail count=%d, want 1", count)
	}
}

func TestLoginUserNotFoundUnauthorizedAndIncr(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	clearAuthTables(t, db)
	clearLoginFailKeys(t, rdb)

	r := newLoginRouter(db, rdb)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "notfound@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
	key := "login_fail:192.0.2.1:notfound@example.com"
	count, err := rdb.Get(context.Background(), key).Int()
	if err != nil {
		t.Fatalf("redis get fail count: %v", err)
	}
	if count != 1 {
		t.Fatalf("fail count=%d, want 1", count)
	}
}

func TestLoginRateLimited(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	clearAuthTables(t, db)
	clearLoginFailKeys(t, rdb)

	r := newLoginRouter(db, rdb)
	key := "login_fail:192.0.2.1:limited@example.com"
	if err := rdb.Set(context.Background(), key, "5", 10*time.Minute).Err(); err != nil {
		t.Fatalf("preset rate-limit key: %v", err)
	}
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "limited@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusTooManyRequests)
	}

	var body struct {
		Code int `json:"code"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.RateLimited {
		t.Fatalf("code=%d, want rate_limited", body.Code)
	}
}

func newLoginRouter(db *pgxpool.Pool, rdb *goredis.Client) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := &Handler{
		Store:           &store.Store{DB: db},
		Redis:           rdb,
		JWTIssuer:       "anvilkit-auth",
		JWTAudience:     "anvilkit-clients",
		JWTSecret:       "test-secret",
		AccessTTL:       15 * time.Minute,
		RefreshTTL:      168 * time.Hour,
		LoginFailLimit:  5,
		LoginFailWindow: 10 * time.Minute,
	}
	r.POST("/v1/auth/login", func(c *gin.Context) { c.Request.RemoteAddr = "192.0.2.1:12345"; ginmid.Wrap(h.Login)(c) })
	return r
}

func seedLoginUser(t *testing.T, db *pgxpool.Pool, email, password string, status int16) {
	t.Helper()
	id := strings.ReplaceAll(email, "@", "-")
	h, err := crypto.HashPassword(password, 4)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	_, err = db.Exec(context.Background(), `insert into users(id,email,status,created_at,updated_at) values($1,$2,$3,now(),now())`, id, email, status)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = db.Exec(context.Background(), `insert into user_password_credentials(user_id,password_hash,updated_at) values($1,$2,now())`, id, h)
	if err != nil {
		t.Fatalf("insert credential: %v", err)
	}
}

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
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func clearLoginFailKeys(t *testing.T, rdb *goredis.Client) {
	t.Helper()
	keys, err := rdb.Keys(context.Background(), "login_fail:*").Result()
	if err != nil {
		t.Fatalf("redis keys: %v", err)
	}
	if len(keys) > 0 {
		if err = rdb.Del(context.Background(), keys...).Err(); err != nil {
			t.Fatalf("redis del keys: %v", err)
		}
	}
}
