package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	ajwt "anvilkit-auth-template/modules/common-go/pkg/auth/jwt"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/admin-api/internal/handler"
	"anvilkit-auth-template/services/admin-api/internal/rbac"
	"anvilkit-auth-template/services/admin-api/internal/store"
	"anvilkit-auth-template/services/admin-api/internal/testutil"
)

func TestAdminRBACMiddleware(t *testing.T) {
	db := mustTestDB(t)
	truncateTables(t, db)

	tenantID := "tenant-alpha"
	tenant2ID := "tenant-beta"
	ownerID := uuid.NewString()
	adminID := uuid.NewString()
	memberID := uuid.NewString()
	tenant2OwnerID := uuid.NewString()

	seed(t, db, tenantID, ownerID, adminID, memberID, uuid.NewString(), tenant2ID, tenant2OwnerID)

	r := newTestRouter(t, db)

	t.Run("unauthorized without bearer", func(t *testing.T) {
		w := performJSONNoToken(r, http.MethodGet, "/api/v1/admin/tenants/"+tenantID+"/members", nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("want 401 got %d body=%s", w.Code, w.Body.String())
		}
		assertEnvelopeMessage(t, w, "unauthorized")
	})

	t.Run("forbidden for member role", func(t *testing.T) {
		memberToken := mustAccessToken(t, memberID, &tenantID)
		w := performJSON(r, http.MethodGet, "/api/v1/admin/tenants/"+tenantID+"/members", memberToken, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("want 403 got %d body=%s", w.Code, w.Body.String())
		}
		assertEnvelopeMessage(t, w, "forbidden")
	})

	t.Run("owner allowed", func(t *testing.T) {
		ownerToken := mustAccessToken(t, ownerID, &tenantID)
		w := performJSON(r, http.MethodGet, "/api/v1/admin/tenants/"+tenantID+"/members", ownerToken, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("admin allowed", func(t *testing.T) {
		adminToken := mustAccessToken(t, adminID, &tenantID)
		newUID := uuid.NewString()
		_, err := db.Exec(context.Background(), `insert into users(id,email,password_hash) values ($1,$2,$3)`, newUID, "new-admin-action@example.com", "hash")
		if err != nil {
			t.Fatalf("insert user: %v", err)
		}
		w := performJSON(r, http.MethodPost, "/api/v1/admin/tenants/"+tenantID+"/members", adminToken, map[string]string{"user_id": newUID, "role": "member"})
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("tenant mismatch forbidden", func(t *testing.T) {
		mismatchToken := mustAccessToken(t, ownerID, &tenant2ID)
		w := performJSON(r, http.MethodGet, "/api/v1/admin/tenants/"+tenantID+"/members", mismatchToken, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("want 403 got %d body=%s", w.Code, w.Body.String())
		}
		assertEnvelopeMessage(t, w, "forbidden")
	})
}

func TestMemberManagementEndpoints(t *testing.T) {
	db := mustTestDB(t)
	truncateTables(t, db)

	tenantID := "tenant-alpha"
	ownerID := uuid.NewString()
	memberID := uuid.NewString()
	targetID := uuid.NewString()
	otherTenantID := "tenant-beta"
	otherOwnerID := uuid.NewString()

	seed(t, db, tenantID, ownerID, uuid.NewString(), memberID, targetID, otherTenantID, otherOwnerID)

	r := newTestRouter(t, db)

	ownerToken := mustAccessToken(t, ownerID, &tenantID)

	t.Run("owner can add member", func(t *testing.T) {
		newUID := uuid.NewString()
		_, err := db.Exec(context.Background(), `insert into users(id,email,password_hash) values ($1,$2,$3)`, newUID, "new@example.com", "hash")
		if err != nil {
			t.Fatalf("insert user: %v", err)
		}
		w := performJSON(r, http.MethodPost, "/api/v1/admin/tenants/"+tenantID+"/members", ownerToken, map[string]string{"user_id": newUID, "role": "member"})
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("owner can patch role", func(t *testing.T) {
		w := performJSON(r, http.MethodPatch, "/api/v1/admin/tenants/"+tenantID+"/members/"+targetID, ownerToken, map[string]string{"role": "admin"})
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("owner can delete member", func(t *testing.T) {
		w := performJSON(r, http.MethodDelete, "/api/v1/admin/tenants/"+tenantID+"/members/"+targetID, ownerToken, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
		}
	})
}

func newTestRouter(t *testing.T, db *pgxpool.Pool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	enforcer, err := rbac.NewEnforcer(os.Getenv("TEST_DB_DSN"), modelPath())
	if err != nil {
		t.Fatalf("rbac.NewEnforcer: %v", err)
	}
	h := &handler.Handler{Store: &store.Store{DB: db}, Enforcer: enforcer}
	r := gin.New()
	r.Use(ginmid.ErrorHandler())
	admin := r.Group("/api/v1/admin", ginmid.AuthN("test-secret-only", "anvilkit-auth", "anvilkit-clients"), handler.AdminRBAC(h.Store, enforcer))
	admin.GET("/tenants/:tenantId/members", ginmid.Wrap(h.ListMembers))
	admin.POST("/tenants/:tenantId/members", ginmid.Wrap(h.AddMember))
	admin.PATCH("/tenants/:tenantId/members/:uid", ginmid.Wrap(h.UpdateMemberRole))
	admin.DELETE("/tenants/:tenantId/members/:uid", ginmid.Wrap(h.RemoveMember))
	return r
}

func performJSON(r http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			panic(err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func performJSONNoToken(r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			panic(err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func assertEnvelopeMessage(t *testing.T, w *httptest.ResponseRecorder, msg string) {
	t.Helper()
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, w.Body.String())
	}
	if body.Message != msg {
		t.Fatalf("want message=%s got=%s", msg, body.Message)
	}
}

func mustAccessToken(t *testing.T, uid string, tid *string) string {
	t.Helper()
	tok, err := ajwt.SignAccessToken("test-secret-only", "anvilkit-auth", "anvilkit-clients", uid, tid, time.Hour)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

func mustTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testutil.MustTestDB(t)
}

func truncateTables(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	testutil.TruncateAuthTables(t, db)
	if _, err := db.Exec(context.Background(), `TRUNCATE TABLE casbin_rule RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate casbin_rule: %v", err)
	}
}

func modelPath() string {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", ".."))
	return filepath.Join(root, "services", "admin-api", "internal", "rbac", "model.conf")
}

func seed(t *testing.T, db *pgxpool.Pool, tenantID, ownerID, adminID, memberID, targetID, otherTenantID, otherOwnerID string) {
	t.Helper()

	if _, err := db.Exec(context.Background(),
		`insert into tenants(id,name) values ($1,'Tenant A'), ($2,'Tenant B')`,
		tenantID, otherTenantID,
	); err != nil {
		t.Fatalf("seed tenants: %v", err)
	}

	if _, err := db.Exec(context.Background(),
		`insert into users(id,email,password_hash) values
  ($1,'owner@example.com','hash'),
  ($2,'admin@example.com','hash'),
  ($3,'member@example.com','hash'),
  ($4,'target@example.com','hash'),
  ($5,'other-owner@example.com','hash')`,
		ownerID, adminID, memberID, targetID, otherOwnerID,
	); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	if _, err := db.Exec(context.Background(),
		`insert into tenant_users(tenant_id,user_id,role) values
  ($1,$2,'owner'),
  ($1,$3,'admin'),
  ($1,$4,'member'),
  ($1,$5,'member'),
  ($6,$7,'owner')`,
		tenantID, ownerID, adminID, memberID, targetID, otherTenantID, otherOwnerID,
	); err != nil {
		t.Fatalf("seed tenant_users: %v", err)
	}
}
