package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/store"
	"anvilkit-auth-template/services/auth-api/internal/testutil"
)

func TestRegisterSuccess(t *testing.T) {
	db := newTestDB(t)
	testutil.TruncateAuthTables(t, db)

	r := newRegisterRouter(db, 8)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "user1@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusCreated, res.Body.String())
	}

	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			User struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"user"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != 0 || body.Message != "ok" {
		t.Fatalf("unexpected envelope: %+v", body)
	}
	if body.Data.User.ID == "" {
		t.Fatal("user.id should not be empty")
	}
	if body.Data.User.Email != "user1@example.com" {
		t.Fatalf("user.email = %q, want %q", body.Data.User.Email, "user1@example.com")
	}

	var usersCount, credCount int
	if err := db.QueryRow(context.Background(), "select count(1) from users where email=$1", "user1@example.com").Scan(&usersCount); err != nil {
		t.Fatalf("query users count: %v", err)
	}
	if err := db.QueryRow(context.Background(), "select count(1) from user_password_credentials").Scan(&credCount); err != nil {
		t.Fatalf("query credential count: %v", err)
	}
	if usersCount != 1 || credCount != 1 {
		t.Fatalf("usersCount=%d credCount=%d, want both 1", usersCount, credCount)
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	db := newTestDB(t)
	testutil.TruncateAuthTables(t, db)

	r := newRegisterRouter(db, 8)
	performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "dup@example.com",
		"password": "Passw0rd!",
	})
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "dup@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusConflict, res.Body.String())
	}
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Conflict || body.Message != "conflict" {
		t.Fatalf("unexpected conflict response: %+v", body)
	}
}

func TestRegisterWeakPassword(t *testing.T) {
	db := newTestDB(t)
	testutil.TruncateAuthTables(t, db)

	r := newRegisterRouter(db, 10)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "weak@example.com",
		"password": "short",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.BadRequest || body.Message != "bad_request" {
		t.Fatalf("unexpected bad request response: %+v", body)
	}

	var usersCount int
	if err := db.QueryRow(context.Background(), "select count(1) from users where email=$1", "weak@example.com").Scan(&usersCount); err != nil {
		t.Fatalf("query users count: %v", err)
	}
	if usersCount != 0 {
		t.Fatalf("usersCount = %d, want 0", usersCount)
	}
}

func newRegisterRouter(db *pgxpool.Pool, passwordMinLen int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := &Handler{Store: &store.Store{DB: db}, PasswordMinLen: passwordMinLen, BcryptCost: 4}
	r.POST("/v1/auth/register", ginmid.Wrap(h.Register))
	return r
}
