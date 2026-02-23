package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	ajwt "anvilkit-auth-template/modules/common-go/pkg/auth/jwt"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/testutil"
)

func TestSwitchTenantSuccessReturnsTokenWithTID(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)

	uid := uuid.NewString()
	tenantID := uuid.NewString()
	seedAuthUser(t, db, uid, "switch-ok@example.com")
	_, err := db.Exec(context.Background(), `insert into tenants(id,name,created_at) values($1,$2,now())`, tenantID, "Acme")
	if err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	_, err = db.Exec(context.Background(), `insert into tenant_users(tenant_id,user_id,role,created_at) values($1,$2,'member',now())`, tenantID, uid)
	if err != nil {
		t.Fatalf("insert tenant_users: %v", err)
	}

	h := newTestAuthHandler(t, db, rdb)
	oldToken, err := ajwt.SignAccessToken(h.JWTSecret, h.JWTIssuer, h.JWTAudience, uid, nil, time.Minute)
	if err != nil {
		t.Fatalf("sign access token: %v", err)
	}

	r := newSwitchTenantRouter(h)
	res := performAuthedJSONRequest(t, r, http.MethodPost, "/v1/auth/switch_tenant", oldToken, map[string]string{"tenant_id": tenantID})
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusOK, res.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int    `json:"expires_in"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != 0 || body.Data.AccessToken == "" {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.Data.ExpiresIn != 900 {
		t.Fatalf("expires_in=%d want=900", body.Data.ExpiresIn)
	}

	claims, err := ajwt.Parse(h.JWTSecret, h.JWTIssuer, h.JWTAudience, body.Data.AccessToken)
	if err != nil {
		t.Fatalf("parse switched token: %v", err)
	}
	if claims.TID != tenantID {
		t.Fatalf("claims.tid=%q want=%q", claims.TID, tenantID)
	}
}

func TestSwitchTenantForbiddenWhenNotInTenant(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)

	uid := uuid.NewString()
	tenantID := uuid.NewString()
	seedAuthUser(t, db, uid, "switch-deny@example.com")
	_, err := db.Exec(context.Background(), `insert into tenants(id,name,created_at) values($1,$2,now())`, tenantID, "NoMembership")
	if err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	h := newTestAuthHandler(t, db, rdb)
	token, err := ajwt.SignAccessToken(h.JWTSecret, h.JWTIssuer, h.JWTAudience, uid, nil, time.Minute)
	if err != nil {
		t.Fatalf("sign access token: %v", err)
	}

	r := newSwitchTenantRouter(h)
	res := performAuthedJSONRequest(t, r, http.MethodPost, "/v1/auth/switch_tenant", token, map[string]string{"tenant_id": tenantID})
	if res.Code != http.StatusForbidden {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusForbidden, res.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Forbidden {
		t.Fatalf("code=%d want=%d", body.Code, errcode.Forbidden)
	}
	if body.Data.Reason != "not_in_tenant" {
		t.Fatalf("reason=%q want=not_in_tenant", body.Data.Reason)
	}
}

func newSwitchTenantRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	r.POST("/v1/auth/switch_tenant", ginmid.AuthN(h.JWTSecret, h.JWTIssuer, h.JWTAudience), ginmid.Wrap(h.SwitchTenant))
	return r
}

func seedAuthUser(t *testing.T, db *pgxpool.Pool, uid, email string) {
	t.Helper()
	_, err := db.Exec(context.Background(), `insert into users(id,email,status,created_at,updated_at) values($1,$2,1,now(),now())`, uid, email)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
}
