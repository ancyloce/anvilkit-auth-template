package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMarkSent_UpdatesRecordAndInsertsHistory(t *testing.T) {
	db := mustTestDB(t)
	truncateEmailTables(t, db)

	recordID := "rec-mark-sent"
	if _, err := db.Exec(context.Background(), `
insert into email_records(id,to_email,status,created_at,updated_at)
values($1,$2,'queued',now(),now())`,
		recordID,
		"user@example.com",
	); err != nil {
		t.Fatalf("insert email record: %v", err)
	}

	s := &Store{DB: db}
	if err := s.MarkSent(context.Background(), recordID, "esp-123"); err != nil {
		t.Fatalf("mark sent: %v", err)
	}

	var externalID, status string
	if err := db.QueryRow(context.Background(), `select external_id,status from email_records where id=$1`, recordID).Scan(&externalID, &status); err != nil {
		t.Fatalf("query email_records: %v", err)
	}
	if externalID != "esp-123" {
		t.Fatalf("external_id=%q want=esp-123", externalID)
	}
	if status != "sent" {
		t.Fatalf("status=%q want=sent", status)
	}

	var historyStatus, historyMessage string
	if err := db.QueryRow(context.Background(), `
select status,message
from email_status_history
where email_record_id=$1
order by created_at desc
limit 1`,
		recordID,
	).Scan(&historyStatus, &historyMessage); err != nil {
		t.Fatalf("query status history: %v", err)
	}
	if historyStatus != "sent" {
		t.Fatalf("history status=%q want=sent", historyStatus)
	}
	if strings.TrimSpace(historyMessage) == "" {
		t.Fatal("history message should not be empty")
	}
}

func TestMarkFailed_UpdatesRecordAndInsertsHistory(t *testing.T) {
	db := mustTestDB(t)
	truncateEmailTables(t, db)

	recordID := "rec-mark-failed"
	if _, err := db.Exec(context.Background(), `
insert into email_records(id,to_email,status,created_at,updated_at)
values($1,$2,'queued',now(),now())`,
		recordID,
		"user@example.com",
	); err != nil {
		t.Fatalf("insert email record: %v", err)
	}

	s := &Store{DB: db}
	if err := s.MarkFailed(context.Background(), recordID, "smtp timeout"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	var status string
	if err := db.QueryRow(context.Background(), `select status from email_records where id=$1`, recordID).Scan(&status); err != nil {
		t.Fatalf("query email_records: %v", err)
	}
	if status != "failed" {
		t.Fatalf("status=%q want=failed", status)
	}

	var historyStatus, historyMessage string
	if err := db.QueryRow(context.Background(), `
select status,message
from email_status_history
where email_record_id=$1
order by created_at desc
limit 1`,
		recordID,
	).Scan(&historyStatus, &historyMessage); err != nil {
		t.Fatalf("query status history: %v", err)
	}
	if historyStatus != "failed" {
		t.Fatalf("history status=%q want=failed", historyStatus)
	}
	if historyMessage != "smtp timeout" {
		t.Fatalf("history message=%q want=smtp timeout", historyMessage)
	}
}

func mustTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DB_DSN"))
	if dsn == "" {
		t.Skip("skip integration test: TEST_DB_DSN is not set")
	}

	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if err := db.Ping(context.Background()); err != nil {
		db.Close()
		t.Fatalf("db ping: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	applyMigrations(t, db)
	return db
}

func applyMigrations(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	for _, name := range []string{"001_init.sql", "002_authn_core.sql", "003_multitenant.sql", "004_email_service.sql", "005_email_verifications_token_hash_scope.sql"} {
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

func truncateEmailTables(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `
truncate table
  email_status_history,
  email_records,
  email_jobs,
  email_verifications
restart identity cascade`); err != nil {
		t.Fatalf("truncate email tables: %v", err)
	}
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "auth-api", "migrations"))
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("migrations dir not found: %s (%v)", dir, err)
	}
	return dir
}
