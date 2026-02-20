package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/store"
)

func TestLoginV1Success(t *testing.T) {
	db := newTestDB(t)
	clearAuthTables(t, db)
	rdb := newTestRedis(t)
	clearTestRedis(t, rdb)

	r := newLoginRouter(db, rdb, 8, 5, 10*time.Minute)

	performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "loginuser@example.com",
		"password": "Passw0rd!",
	})

	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "loginuser@example.com",
		"password": "Passw0rd!",
	})

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusOK, res.Body.String())
	}

	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
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

	if body.Code != 0 || body.Message != "ok" {
		t.Fatalf("unexpected envelope: %+v", body)
	}
	if body.Data.AccessToken == "" {
		t.Fatal("access_token should not be empty")
	}
	if body.Data.RefreshToken == "" {
		t.Fatal("refresh_token should not be empty")
	}
	if body.Data.ExpiresIn <= 0 {
		t.Fatal("expires_in should be positive")
	}
	if body.Data.RefreshExpiresIn <= 0 {
		t.Fatal("refresh_expires_in should be positive")
	}
	if body.Data.User.ID == "" {
		t.Fatal("user.id should not be empty")
	}
	if body.Data.User.Email != "loginuser@example.com" {
		t.Fatalf("user.email = %q, want %q", body.Data.User.Email, "loginuser@example.com")
	}

	var tokenCount int
	if err := db.QueryRow(context.Background(), "select count(1) from refresh_tokens where user_id=$1", body.Data.User.ID).Scan(&tokenCount); err != nil {
		t.Fatalf("query refresh_tokens count: %v", err)
	}
	if tokenCount != 1 {
		t.Fatalf("tokenCount=%d, want 1", tokenCount)
	}
}

func TestLoginV1WrongPassword(t *testing.T) {
	db := newTestDB(t)
	clearAuthTables(t, db)
	rdb := newTestRedis(t)
	clearTestRedis(t, rdb)

	r := newLoginRouter(db, rdb, 8, 5, 10*time.Minute)

	performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "wrongpw@example.com",
		"password": "Passw0rd!",
	})

	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "wrongpw@example.com",
		"password": "WrongPassword!",
	})

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusUnauthorized, res.Body.String())
	}

	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Unauthorized || body.Message != "unauthorized" {
		t.Fatalf("unexpected unauthorized response: %+v", body)
	}

	count, err := rdb.Get(context.Background(), "login_fail:192.0.2.1:wrongpw@example.com").Int()
	if err != nil {
		t.Fatalf("redis get: %v", err)
	}
	if count != 1 {
		t.Fatalf("redis fail count = %d, want 1", count)
	}
}

func TestLoginV1UserNotFound(t *testing.T) {
	db := newTestDB(t)
	clearAuthTables(t, db)
	rdb := newTestRedis(t)
	clearTestRedis(t, rdb)

	r := newLoginRouter(db, rdb, 8, 5, 10*time.Minute)

	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "notfound@example.com",
		"password": "Passw0rd!",
	})

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusUnauthorized, res.Body.String())
	}

	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Unauthorized || body.Message != "unauthorized" {
		t.Fatalf("unexpected unauthorized response: %+v", body)
	}

	count, err := rdb.Get(context.Background(), "login_fail:192.0.2.1:notfound@example.com").Int()
	if err != nil {
		t.Fatalf("redis get: %v", err)
	}
	if count != 1 {
		t.Fatalf("redis fail count = %d, want 1", count)
	}
}

func TestLoginV1RateLimited(t *testing.T) {
	db := newTestDB(t)
	clearAuthTables(t, db)
	rdb := newTestRedis(t)
	clearTestRedis(t, rdb)

	r := newLoginRouter(db, rdb, 8, 3, 10*time.Minute)

	performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "ratelimit@example.com",
		"password": "Passw0rd!",
	})

	for i := 0; i < 3; i++ {
		performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
			"email":    "ratelimit@example.com",
			"password": "WrongPassword!",
		})
	}

	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "ratelimit@example.com",
		"password": "WrongPassword!",
	})

	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusTooManyRequests, res.Body.String())
	}

	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.RateLimited || body.Message != "rate_limited" {
		t.Fatalf("unexpected rate limited response: %+v", body)
	}
}

func TestLoginV1ClearsRateLimitOnSuccess(t *testing.T) {
	db := newTestDB(t)
	clearAuthTables(t, db)
	rdb := newTestRedis(t)
	clearTestRedis(t, rdb)

	r := newLoginRouter(db, rdb, 8, 5, 10*time.Minute)

	performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "clearcount@example.com",
		"password": "Passw0rd!",
	})

	for i := 0; i < 2; i++ {
		performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
			"email":    "clearcount@example.com",
			"password": "WrongPassword!",
		})
	}

	count, err := rdb.Get(context.Background(), "login_fail:192.0.2.1:clearcount@example.com").Int()
	if err != nil {
		t.Fatalf("redis get before success: %v", err)
	}
	if count != 2 {
		t.Fatalf("redis fail count before success = %d, want 2", count)
	}

	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "clearcount@example.com",
		"password": "Passw0rd!",
	})

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusOK, res.Body.String())
	}

	_, err = rdb.Get(context.Background(), "login_fail:192.0.2.1:clearcount@example.com").Result()
	if err == nil {
		t.Fatal("rate limit key should be deleted after successful login")
	}
}

func newLoginRouter(db *pgxpool.Pool, rdb *goredis.Client, passwordMinLen, loginFailLimit int, loginFailWindow time.Duration) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := &Handler{
		Store:           &store.Store{DB: db},
		RedisClient:     rdb,
		JWTSecret:       "test-secret",
		JWTIssuer:       "test-issuer",
		JWTAudience:     "test-audience",
		AccessTTL:       15 * time.Minute,
		RefreshTTL:      168 * time.Hour,
		PasswordMinLen:  passwordMinLen,
		BcryptCost:      4,
		LoginFailLimit:  loginFailLimit,
		LoginFailWindow: loginFailWindow,
	}
	r.POST("/v1/auth/register", ginmid.Wrap(h.Register))
	r.POST("/v1/auth/login", ginmid.Wrap(h.LoginV1))
	return r
}

func newTestRedis(t *testing.T) *goredis.Client {
	t.Helper()
	addr := "localhost:6379"
	rdb := goredis.NewClient(&goredis.Options{Addr: addr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis not available at %s: %v", addr, err)
	}
	t.Cleanup(func() { rdb.Close() })
	return rdb
}

func clearTestRedis(t *testing.T, rdb *goredis.Client) {
	t.Helper()
	keys, err := rdb.Keys(context.Background(), "login_fail:*").Result()
	if err != nil {
		t.Fatalf("redis keys: %v", err)
	}
	if len(keys) > 0 {
		if err := rdb.Del(context.Background(), keys...).Err(); err != nil {
			t.Fatalf("redis del: %v", err)
		}
	}
}
