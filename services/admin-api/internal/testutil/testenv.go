package testutil

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func MustTestDB(t *testing.T) *pgxpool.Pool {
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
		db.Close()
		t.Fatalf("db ping: %v", err)
	}

	lockConn := lockTestDB(t, db)
	t.Cleanup(func() {
		unlockTestDB(t, lockConn)
		lockConn.Release()
		db.Close()
	})

	ApplyMigrations(t, db)
	return db
}

func ApplyMigrations(t *testing.T, db *pgxpool.Pool) {
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

func TruncateAuthTables(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	_, err := db.Exec(context.Background(), `TRUNCATE TABLE user_roles, tenant_users, refresh_tokens, refresh_sessions, user_password_credentials, tenants, users RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate auth tables: %v", err)
	}
}

func lockTestDB(t *testing.T, db *pgxpool.Pool) *pgxpool.Conn {
	t.Helper()
	conn, err := db.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire lock conn: %v", err)
	}

	deadlineCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		var locked bool
		if err = conn.QueryRow(deadlineCtx, `select pg_try_advisory_lock($1)`, int64(240809)).Scan(&locked); err != nil {
			conn.Release()
			t.Fatalf("pg_try_advisory_lock: %v", err)
		}
		if locked {
			return conn
		}
		select {
		case <-deadlineCtx.Done():
			conn.Release()
			t.Fatalf("timed out waiting for advisory lock for test db after 15s")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func unlockTestDB(t *testing.T, conn *pgxpool.Conn) {
	t.Helper()
	if _, err := conn.Exec(context.Background(), `select pg_advisory_unlock($1)`, int64(240809)); err != nil {
		t.Errorf("pg_advisory_unlock: %v", err)
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

func repoRoot() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", os.ErrNotExist
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "..")), nil
}
