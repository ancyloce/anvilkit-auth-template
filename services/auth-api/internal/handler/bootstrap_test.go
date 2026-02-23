package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/testutil"
)

func TestBootstrapSuccessAndLogin(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)

	r := newBootstrapRouter(t, db, rdb)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/bootstrap", map[string]string{
		"tenant_name":    "Acme",
		"owner_email":    "owner@example.com",
		"owner_password": "Passw0rd!",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusCreated, res.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			Tenant struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"tenant"`
			OwnerUser struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"owner_user"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != 0 {
		t.Fatalf("unexpected envelope code: %d", body.Code)
	}
	if body.Data.Tenant.ID == "" || body.Data.OwnerUser.ID == "" {
		t.Fatalf("tenant/user id should not be empty: %+v", body.Data)
	}
	if body.Data.Tenant.Name != "Acme" || body.Data.OwnerUser.Email != "owner@example.com" {
		t.Fatalf("unexpected response data: %+v", body.Data)
	}

	var tenantCount int
	if err := db.QueryRow(context.Background(), `select count(1) from tenants where id=$1 and name=$2`, body.Data.Tenant.ID, "Acme").Scan(&tenantCount); err != nil {
		t.Fatalf("query tenant: %v", err)
	}
	if tenantCount != 1 {
		t.Fatalf("tenant count=%d, want 1", tenantCount)
	}

	var relCount int
	if err := db.QueryRow(context.Background(), `select count(1) from tenant_users where tenant_id=$1 and user_id=$2 and role='owner'`, body.Data.Tenant.ID, body.Data.OwnerUser.ID).Scan(&relCount); err != nil {
		t.Fatalf("query tenant_users: %v", err)
	}
	if relCount != 1 {
		t.Fatalf("tenant_users(owner) count=%d, want 1", relCount)
	}

	loginRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "owner@example.com",
		"password": "Passw0rd!",
	})
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body = %s", loginRes.Code, http.StatusOK, loginRes.Body.String())
	}
}

func TestBootstrapTenantNameConflict(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)

	r := newBootstrapRouter(t, db, rdb)
	_ = performJSONRequest(t, r, http.MethodPost, "/v1/bootstrap", map[string]string{
		"tenant_name":    "Acme",
		"owner_email":    "owner@example.com",
		"owner_password": "Passw0rd!",
	})
	res := performJSONRequest(t, r, http.MethodPost, "/v1/bootstrap", map[string]string{
		"tenant_name":    "Acme",
		"owner_email":    "owner@example.com",
		"owner_password": "Passw0rd!",
	})
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusConflict, res.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Conflict || body.Data.Reason != "tenant_name_conflict" {
		t.Fatalf("unexpected conflict body: %+v", body)
	}
}

func TestBootstrapOwnerPasswordMismatch(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)

	r := newBootstrapRouter(t, db, rdb)
	_ = performJSONRequest(t, r, http.MethodPost, "/v1/bootstrap", map[string]string{
		"tenant_name":    "Acme",
		"owner_email":    "owner@example.com",
		"owner_password": "Passw0rd!",
	})
	res := performJSONRequest(t, r, http.MethodPost, "/v1/bootstrap", map[string]string{
		"tenant_name":    "Beta",
		"owner_email":    "owner@example.com",
		"owner_password": "WrongPass1!",
	})
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusUnauthorized, res.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Unauthorized || body.Data.Reason != "owner_password_mismatch" {
		t.Fatalf("unexpected unauthorized body: %+v", body)
	}
}

func newBootstrapRouter(t *testing.T, db *pgxpool.Pool, rdb *goredis.Client) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := newTestAuthHandler(t, db, rdb)
	r.POST("/v1/bootstrap", ginmid.Wrap(h.Bootstrap))
	r.POST("/v1/auth/login", func(c *gin.Context) { c.Request.RemoteAddr = "192.0.2.1:12345"; ginmid.Wrap(h.Login)(c) })
	return r
}
