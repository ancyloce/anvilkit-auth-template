package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

func MustTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DB_DSN"))
	if dsn == "" {
		t.Skip("skip integration test: TEST_DB_DSN is not set")
	}
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if err = db.Ping(context.Background()); err != nil {
		db.Close()
		t.Fatalf("db ping: %v", err)
	}
	lockConn := lockTestDB(t, db)
	t.Cleanup(func() {
		unlockTestDB(t, lockConn)
		lockConn.Release()
	})
	t.Cleanup(func() { db.Close() })
	ApplyMigrations(t, db)
	return db
}

func lockTestDB(t *testing.T, db *pgxpool.Pool) *pgxpool.Conn {
	t.Helper()
	conn, err := db.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire lock conn: %v", err)
	}
	if _, err = conn.Exec(context.Background(), `select pg_advisory_lock($1)`, int64(240808)); err != nil {
		conn.Release()
		t.Fatalf("pg_advisory_lock: %v", err)
	}
	return conn
}

func unlockTestDB(t *testing.T, conn *pgxpool.Conn) {
	t.Helper()
	if _, err := conn.Exec(context.Background(), `select pg_advisory_unlock($1)`, int64(240808)); err != nil {
		t.Fatalf("pg_advisory_unlock: %v", err)
	}
}

func MustTestRedis(t *testing.T) *goredis.Client {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("TEST_REDIS_ADDR"))
	if addr == "" {
		t.Skip("skip integration test: TEST_REDIS_ADDR is not set")
	}
	rdb := goredis.NewClient(&goredis.Options{Addr: addr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		_ = rdb.Close()
		t.Fatalf("redis ping: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func MustJWTSecret(t *testing.T) string {
	t.Helper()
	if s := strings.TrimSpace(os.Getenv("JWT_SECRET")); s != "" {
		return s
	}
	return "test-secret-only"
}

func ApplyMigrations(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	for _, name := range []string{"001_init.sql", "002_authn_core.sql"} {
		sqlPath := filepath.Join(migrationsDir(t), name)
		sqlBytes, err := os.ReadFile(sqlPath)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err = db.Exec(context.Background(), string(sqlBytes)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

func TruncateAuthTables(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	_, err := db.Exec(context.Background(), `
truncate table
  user_roles,
  tenant_users,
  refresh_tokens,
  refresh_sessions,
  user_password_credentials,
  tenants,
  users
restart identity cascade`)
	if err != nil {
		t.Fatalf("truncate auth tables: %v", err)
	}
}

func FlushRedisKeys(t *testing.T, rdb *goredis.Client, pattern string) {
	t.Helper()
	iter := rdb.Scan(context.Background(), 0, pattern, 0).Iterator()
	var keys []string
	for iter.Next(context.Background()) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("redis scan %q: %v", pattern, err)
	}
	if len(keys) > 0 {
		if err := rdb.Del(context.Background(), keys...).Err(); err != nil {
			t.Fatalf("redis del keys: %v", err)
		}
	}
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "migrations"))
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("migrations dir not found: %s (%v)", dir, err)
	}
	return dir
}

func EnvSummary() string {
	return fmt.Sprintf("TEST_DB_DSN=%q TEST_REDIS_ADDR=%q", os.Getenv("TEST_DB_DSN"), os.Getenv("TEST_REDIS_ADDR"))
}
