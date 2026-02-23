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
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	ajwt "anvilkit-auth-template/modules/common-go/pkg/auth/jwt"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/admin-api/internal/handler"
	"anvilkit-auth-template/services/admin-api/internal/store"
)

func TestMemberManagementEndpoints(t *testing.T) {
	db := mustTestDB(t)
	truncateTables(t, db)

	tenantID := "tenant-alpha"
	ownerID := uuid.NewString()
	memberID := uuid.NewString()
	targetID := uuid.NewString()
	otherTenantID := "tenant-beta"
	otherOwnerID := uuid.NewString()

	seed(t, db, tenantID, ownerID, memberID, targetID, otherTenantID, otherOwnerID)

	r := newTestRouter(db)

	ownerToken := mustAccessToken(t, ownerID, &tenantID)
	memberToken := mustAccessToken(t, memberID, &tenantID)
	mismatchToken := mustAccessToken(t, ownerID, &otherTenantID)

	t.Run("owner can list members", func(t *testing.T) {
		w := performJSON(r, http.MethodGet, "/api/v1/admin/tenants/"+tenantID+"/members", ownerToken, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("member forbidden to list", func(t *testing.T) {
		w := performJSON(r, http.MethodGet, "/api/v1/admin/tenants/"+tenantID+"/members", memberToken, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("want 403 got %d body=%s", w.Code, w.Body.String())
		}
	})

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

	t.Run("tenant mismatch forbidden", func(t *testing.T) {
		w := performJSON(r, http.MethodGet, "/api/v1/admin/tenants/"+tenantID+"/members", mismatchToken, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("want 403 got %d body=%s", w.Code, w.Body.String())
		}
	})
}

func newTestRouter(db *pgxpool.Pool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &handler.Handler{Store: &store.Store{DB: db}}
	r := gin.New()
	r.Use(ginmid.ErrorHandler())
	admin := r.Group("/api/v1/admin", ginmid.AuthN("test-secret-only", "anvilkit-auth", "anvilkit-clients"), handler.MustTenantMatch(h.Store))
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
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
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
	dsn := strings.TrimSpace(os.Getenv("TEST_DB_DSN"))
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set")
	}
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	if err = db.Ping(context.Background()); err != nil {
		t.Fatalf("db ping: %v", err)
	}
	applyMigrations(t, db)
	t.Cleanup(func() { db.Close() })
	return db
}

func applyMigrations(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	for _, m := range []string{"001_init.sql", "002_authn_core.sql", "003_multitenant.sql"} {
		sqlBytes, err := os.ReadFile(filepath.Join(migrationsDir(t), m))
		if err != nil {
			t.Fatalf("read migration %s: %v", m, err)
		}
		if _, err = db.Exec(context.Background(), string(sqlBytes)); err != nil {
			t.Fatalf("apply migration %s: %v", m, err)
		}
	}
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "auth-api", "migrations"))
}

func truncateTables(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	_, err := db.Exec(context.Background(), `truncate table user_roles, tenant_users, refresh_tokens, refresh_sessions, user_password_credentials, tenants, users restart identity cascade`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func seed(t *testing.T, db *pgxpool.Pool, tenantID, ownerID, memberID, targetID, otherTenantID, otherOwnerID string) {
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
  ($2,'member@example.com','hash'),
  ($3,'target@example.com','hash'),
  ($4,'other-owner@example.com','hash')`,
		ownerID, memberID, targetID, otherOwnerID,
	); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	if _, err := db.Exec(context.Background(),
		`insert into tenant_users(tenant_id,user_id,role) values
  ($1,$2,'owner'),
  ($1,$3,'member'),
  ($1,$4,'member'),
  ($5,$6,'owner')`,
		tenantID, ownerID, memberID, targetID, otherTenantID, otherOwnerID,
	); err != nil {
		t.Fatalf("seed tenant_users: %v", err)
	}
}
