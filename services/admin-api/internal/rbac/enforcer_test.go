package rbac_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"anvilkit-auth-template/services/admin-api/internal/rbac"
	"anvilkit-auth-template/services/admin-api/internal/testutil"
)

func TestEnforcerWithPostgresAdapter(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_DB_DSN"))
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set")
	}

	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	lockConn := lockDB(t, db)
	t.Cleanup(func() {
		unlockDB(t, lockConn)
		lockConn.Release()
		db.Close()
	})

	if err = db.Ping(context.Background()); err != nil {
		t.Fatalf("db ping: %v", err)
	}

	testutil.ApplyMigrations(t, db)
	if _, err = db.Exec(context.Background(), `TRUNCATE TABLE casbin_rule RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate casbin_rule: %v", err)
	}

	enforcer, err := rbac.NewEnforcer(dsn, modelPath(t))
	if err != nil {
		t.Fatalf("rbac.NewEnforcer: %v", err)
	}

	addedGrouping, err := enforcer.AddGroupingPolicy("tenant_admin", "tenant_admin", "tenant:1")
	if err != nil {
		t.Fatalf("AddGroupingPolicy: %v", err)
	}
	if !addedGrouping {
		t.Fatalf("expected grouping policy to be added")
	}

	if err = enforcer.SavePolicy(); err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}

	enforcer.ClearPolicy()
	if err = enforcer.LoadPolicy(); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}

	ok, err := enforcer.Enforce("tenant_admin", "tenant:1", "/v1/admin/tenants/1/members", "GET")
	if err != nil {
		t.Fatalf("Enforce allow: %v", err)
	}
	if !ok {
		t.Fatalf("expected allow for tenant_admin")
	}

	ok, err = enforcer.Enforce("member", "tenant:1", "/v1/admin/tenants/1/members", "GET")
	if err != nil {
		t.Fatalf("Enforce deny: %v", err)
	}
	if ok {
		t.Fatalf("expected deny for member")
	}
}

func TestSeedDefaultPolicyIdempotent(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_DB_DSN"))
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set")
	}

	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	lockConn := lockDB(t, db)
	t.Cleanup(func() {
		unlockDB(t, lockConn)
		lockConn.Release()
		db.Close()
	})

	testutil.ApplyMigrations(t, db)
	if _, err = db.Exec(context.Background(), `TRUNCATE TABLE casbin_rule RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate casbin_rule: %v", err)
	}

	enforcer, err := rbac.NewEnforcer(dsn, modelPath(t))
	if err != nil {
		t.Fatalf("rbac.NewEnforcer: %v", err)
	}

	var beforeRows int
	if err = db.QueryRow(context.Background(), `select count(*) from casbin_rule`).Scan(&beforeRows); err != nil {
		t.Fatalf("count casbin_rule before: %v", err)
	}

	if beforeRows == 0 {
		t.Fatalf("expected seeded policies from NewEnforcer")
	}

	changed, err := rbac.SeedDefaultPolicy(enforcer)
	if err != nil {
		t.Fatalf("SeedDefaultPolicy call: %v", err)
	}
	if changed {
		t.Fatalf("expected no change on second seed")
	}

	var afterRows int
	if err = db.QueryRow(context.Background(), `select count(*) from casbin_rule`).Scan(&afterRows); err != nil {
		t.Fatalf("count casbin_rule after: %v", err)
	}
	if beforeRows != afterRows {
		t.Fatalf("policy row count changed on idempotent seed: before=%d after=%d", beforeRows, afterRows)
	}

	ownerAllowed, err := enforcer.Enforce("tenant_owner", "tenant:tenant-1", "/v1/admin/tenants/tenant-1/members", "GET")
	if err != nil {
		t.Fatalf("Enforce owner: %v", err)
	}
	if !ownerAllowed {
		t.Fatalf("expected tenant_owner to be allowed")
	}

	memberAllowed, err := enforcer.Enforce("member", "tenant:tenant-1", "/v1/admin/tenants/tenant-1/members", "GET")
	if err != nil {
		t.Fatalf("Enforce member: %v", err)
	}
	if memberAllowed {
		t.Fatalf("expected member to be denied")
	}
}

func TestDefaultPolicyEnforce(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_DB_DSN"))
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set")
	}

	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	lockConn := lockDB(t, db)
	t.Cleanup(func() {
		unlockDB(t, lockConn)
		lockConn.Release()
		db.Close()
	})

	testutil.ApplyMigrations(t, db)
	if _, err = db.Exec(context.Background(), `TRUNCATE TABLE casbin_rule RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate casbin_rule: %v", err)
	}

	enforcer, err := rbac.NewEnforcer(dsn, modelPath(t))
	if err != nil {
		t.Fatalf("rbac.NewEnforcer: %v", err)
	}

	ownerAllowed, err := enforcer.Enforce("tenant_owner", "tenant:1", "/v1/admin/tenants/1/members", "GET")
	if err != nil {
		t.Fatalf("Enforce owner: %v", err)
	}
	if !ownerAllowed {
		t.Fatalf("expected tenant_owner to be allowed")
	}

	adminAllowed, err := enforcer.Enforce("tenant_admin", "tenant:1", "/v1/admin/tenants/1/members", "POST")
	if err != nil {
		t.Fatalf("Enforce admin: %v", err)
	}
	if !adminAllowed {
		t.Fatalf("expected tenant_admin to be allowed")
	}

	memberAllowed, err := enforcer.Enforce("member", "tenant:1", "/v1/admin/tenants/1/members", "GET")
	if err != nil {
		t.Fatalf("Enforce member: %v", err)
	}
	if memberAllowed {
		t.Fatalf("expected member to be denied")
	}
}

func TestMapTenantRoleToCasbin(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "owner", in: "owner", want: "tenant_owner"},
		{name: "admin", in: "admin", want: "tenant_admin"},
		{name: "member", in: "member", want: "member"},
		{name: "invalid", in: "invalid_role", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rbac.MapTenantRoleToCasbin(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("MapTenantRoleToCasbin: %v", err)
			}
			if got != tt.want {
				t.Fatalf("want %s got %s", tt.want, got)
			}
		})
	}
}

func modelPath(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	return filepath.Join(root, "services", "admin-api", "internal", "rbac", "model.conf")
}

func repoRoot() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", os.ErrNotExist
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "..")), nil
}

func lockDB(t *testing.T, db *pgxpool.Pool) *pgxpool.Conn {
	t.Helper()
	conn, err := db.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire lock conn: %v", err)
	}
	if _, err = conn.Exec(context.Background(), `select pg_advisory_lock($1)`, int64(240809)); err != nil {
		conn.Release()
		t.Fatalf("pg_advisory_lock: %v", err)
	}
	return conn
}

func unlockDB(t *testing.T, conn *pgxpool.Conn) {
	t.Helper()
	if _, err := conn.Exec(context.Background(), `select pg_advisory_unlock($1)`, int64(240809)); err != nil {
		t.Errorf("pg_advisory_unlock: %v", err)
	}
}
