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

	enforcer.EnableAutoSave(false)
	addedPolicy, err := enforcer.AddPolicy("tenant_admin", "tenant:*", "/v1/admin/*", "GET")
	if err != nil {
		t.Fatalf("AddPolicy: %v", err)
	}
	if !addedPolicy {
		t.Fatalf("expected policy to be added")
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
