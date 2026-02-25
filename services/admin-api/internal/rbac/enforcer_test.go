package rbac_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"anvilkit-auth-template/services/admin-api/internal/rbac"
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
	t.Cleanup(db.Close)

	if err = db.Ping(context.Background()); err != nil {
		t.Fatalf("db ping: %v", err)
	}

	applyMigrations(t, db)
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

func applyMigrations(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	files, err := migrationFiles()
	if err != nil {
		t.Fatalf("migrationFiles: %v", err)
	}
	for _, file := range files {
		sqlBytes, readErr := os.ReadFile(file)
		if readErr != nil {
			t.Fatalf("read migration %s: %v", file, readErr)
		}
		if _, execErr := db.Exec(context.Background(), string(sqlBytes)); execErr != nil {
			t.Fatalf("apply migration %s: %v", file, execErr)
		}
	}
}

func migrationFiles() ([]string, error) {
	root, err := repoRoot()
	if err != nil {
		return nil, err
	}
	patterns := []string{
		filepath.Join(root, "services", "auth-api", "migrations", "*.sql"),
		filepath.Join(root, "services", "admin-api", "migrations", "*.sql"),
	}
	files := make([]string, 0, 8)
	for _, p := range patterns {
		matches, globErr := filepath.Glob(p)
		if globErr != nil {
			return nil, globErr
		}
		files = append(files, matches...)
	}
	sort.Strings(files)
	return files, nil
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
