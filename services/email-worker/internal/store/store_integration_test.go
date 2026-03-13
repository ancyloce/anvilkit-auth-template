package store

import (
	"context"
	"encoding/json"
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
	for _, name := range []string{"001_init.sql", "002_authn_core.sql", "003_multitenant.sql", "004_email_service.sql", "005_email_verifications_token_hash_scope.sql", "006_email_blacklist.sql", "007_email_blacklist_normalization.sql"} {
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
  email_verifications,
  email_blacklist,
  user_roles,
  tenant_users,
  refresh_tokens,
  refresh_sessions,
  user_password_credentials,
  tenants,
  users
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

func TestMarkBounced_StoresBounceMeta(t *testing.T) {
	db := mustTestDB(t)
	truncateEmailTables(t, db)

	recordID := "rec-mark-bounced"
	if _, err := db.Exec(context.Background(), `
insert into email_records(id,to_email,status,created_at,updated_at)
values($1,$2,'queued',now(),now())`,
		recordID,
		"user@example.com",
	); err != nil {
		t.Fatalf("insert email record: %v", err)
	}

	s := &Store{DB: db}
	if err := s.MarkBounced(context.Background(), recordID, "550 mailbox unavailable", "hard", 550, 0); err != nil {
		t.Fatalf("mark bounced: %v", err)
	}

	var status, bounceType string
	var smtpCode int
	if err := db.QueryRow(context.Background(), `
select status, meta->>'bounce_type', (meta->>'smtp_code')::int
from email_status_history
where email_record_id=$1
order by created_at desc
limit 1`, recordID).Scan(&status, &bounceType, &smtpCode); err != nil {
		t.Fatalf("query status history: %v", err)
	}
	if status != "bounced" || bounceType != "hard" || smtpCode != 550 {
		t.Fatalf("got status=%q bounce_type=%q smtp_code=%d", status, bounceType, smtpCode)
	}
}

func TestBlacklistAndIsBlacklisted(t *testing.T) {
	db := mustTestDB(t)
	truncateEmailTables(t, db)

	s := &Store{DB: db}
	if err := s.Blacklist(context.Background(), "User@Example.com", "hard bounce"); err != nil {
		t.Fatalf("blacklist: %v", err)
	}
	blacklisted, err := s.IsBlacklisted(context.Background(), "user@example.com")
	if err != nil {
		t.Fatalf("is blacklisted: %v", err)
	}
	if !blacklisted {
		t.Fatal("expected email to be blacklisted")
	}
}

func TestEmailBlacklistSchema_RejectsUppercaseEmail(t *testing.T) {
	db := mustTestDB(t)
	truncateEmailTables(t, db)

	_, err := db.Exec(context.Background(), `
insert into email_blacklist(id,email,reason,created_at,updated_at)
values($1,$2,$3,now(),now())`,
		"bl-uppercase",
		"User@Example.com",
		"manual insert",
	)
	if err == nil {
		t.Fatal("expected uppercase blacklist insert to fail check constraint")
	}
}

func TestUpsertWebhookStatusByExternalID_UpdatesRecordAndHistory(t *testing.T) {
	db := mustTestDB(t)
	truncateEmailTables(t, db)

	recordID := "rec-webhook-status"
	if _, err := db.Exec(context.Background(), `
insert into email_records(id,to_email,external_id,status,created_at,updated_at)
values($1,$2,$3,'sent',now(),now())`,
		recordID,
		"user@example.com",
		"esp-webhook-1",
	); err != nil {
		t.Fatalf("insert email record: %v", err)
	}

	s := &Store{DB: db}
	inserted, err := s.UpsertWebhookStatusByExternalID(context.Background(), "esp-webhook-1", "delivered", "accepted", "evt-1", map[string]any{"source": "esp"})
	if err != nil {
		t.Fatalf("upsert delivered: %v", err)
	}
	if !inserted {
		t.Fatal("inserted=false want true")
	}
	inserted, err = s.UpsertWebhookStatusByExternalID(context.Background(), "esp-webhook-1", "opened", "opened by user", "evt-2", nil)
	if err != nil {
		t.Fatalf("upsert opened: %v", err)
	}
	if !inserted {
		t.Fatal("inserted=false want true")
	}

	var status string
	if err := db.QueryRow(context.Background(), `select status from email_records where id=$1`, recordID).Scan(&status); err != nil {
		t.Fatalf("query email_records: %v", err)
	}
	if status != "opened" {
		t.Fatalf("status=%q want=opened", status)
	}

	rows, err := db.Query(context.Background(), `
select status, meta
from email_status_history
where email_record_id=$1
order by created_at asc`, recordID)
	if err != nil {
		t.Fatalf("query status history: %v", err)
	}
	defer rows.Close()

	var history []string
	for rows.Next() {
		var status string
		var metaRaw []byte
		if err := rows.Scan(&status, &metaRaw); err != nil {
			t.Fatalf("scan history: %v", err)
		}
		history = append(history, status)
		var meta map[string]any
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			t.Fatalf("unmarshal meta: %v", err)
		}
		if meta["event_id"] == nil {
			t.Fatalf("meta missing event_id for status=%s", status)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}

	if len(history) != 2 || history[0] != "delivered" || history[1] != "opened" {
		t.Fatalf("history=%v want=[delivered opened]", history)
	}
}

func TestUpsertWebhookStatusByExternalID_DuplicateEventIDIsIdempotent(t *testing.T) {
	db := mustTestDB(t)
	truncateEmailTables(t, db)

	recordID := "rec-webhook-dup"
	if _, err := db.Exec(context.Background(), `
insert into email_records(id,to_email,external_id,status,created_at,updated_at)
values($1,$2,$3,'sent',now(),now())`,
		recordID,
		"user@example.com",
		"esp-webhook-dup",
	); err != nil {
		t.Fatalf("insert email record: %v", err)
	}

	s := &Store{DB: db}
	inserted, err := s.UpsertWebhookStatusByExternalID(context.Background(), "esp-webhook-dup", "delivered", "first", "evt-dup", nil)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !inserted {
		t.Fatal("first inserted=false want true")
	}
	inserted, err = s.UpsertWebhookStatusByExternalID(context.Background(), "esp-webhook-dup", "delivered", "duplicate", "evt-dup", nil)
	if err != nil {
		t.Fatalf("duplicate call: %v", err)
	}
	if inserted {
		t.Fatal("duplicate inserted=true want false")
	}

	var count int
	if err := db.QueryRow(context.Background(), `
select count(*)
from email_status_history
where email_record_id=$1 and status='delivered' and meta->>'event_id'='evt-dup'`, recordID).Scan(&count); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if count != 1 {
		t.Fatalf("count=%d want=1", count)
	}
}

func TestLookupAnalyticsRecordByExternalIDPrefersMostRecentlyUpdatedRecord(t *testing.T) {
	db := mustTestDB(t)
	truncateEmailTables(t, db)

	if _, err := db.Exec(context.Background(), `
insert into users(id,email,status,created_at,updated_at)
values
  ('user-old','old@example.com',1,now()-interval '10 minutes',now()-interval '10 minutes'),
  ('user-new','new@example.com',1,now()-interval '5 minutes',now()-interval '1 minute')`); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	if _, err := db.Exec(context.Background(), `
insert into email_records(id,user_id,to_email,external_id,status,created_at,updated_at)
values
  ('rec-old','user-old','old@example.com','esp-shared','sent',now()-interval '10 minutes',now()-interval '10 minutes'),
  ('rec-new','user-new','new@example.com','esp-shared','sent',now()-interval '5 minutes',now()-interval '1 minute')`); err != nil {
		t.Fatalf("insert email records: %v", err)
	}

	s := &Store{DB: db}
	record, err := s.LookupAnalyticsRecordByExternalID(context.Background(), "esp-shared")
	if err != nil {
		t.Fatalf("LookupAnalyticsRecordByExternalID: %v", err)
	}
	if record.UserID != "user-new" || record.Email != "new@example.com" {
		t.Fatalf("record=%+v want newest record", record)
	}
}

func TestUpsertWebhookStatusByExternalIDPrefersMostRecentlyUpdatedDuplicateRecord(t *testing.T) {
	db := mustTestDB(t)
	truncateEmailTables(t, db)

	if _, err := db.Exec(context.Background(), `
insert into users(id,email,status,created_at,updated_at)
values
  ('user-old-webhook','old-webhook@example.com',1,now()-interval '10 minutes',now()-interval '10 minutes'),
  ('user-new-webhook','new-webhook@example.com',1,now()-interval '5 minutes',now()-interval '1 minute')`); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	if _, err := db.Exec(context.Background(), `
insert into email_records(id,user_id,to_email,external_id,status,created_at,updated_at)
values
  ('rec-webhook-old','user-old-webhook','old-webhook@example.com','esp-dup-webhook','sent',now()-interval '10 minutes',now()-interval '10 minutes'),
  ('rec-webhook-new','user-new-webhook','new-webhook@example.com','esp-dup-webhook','sent',now()-interval '5 minutes',now()-interval '1 minute')`); err != nil {
		t.Fatalf("insert email records: %v", err)
	}

	s := &Store{DB: db}
	inserted, err := s.UpsertWebhookStatusByExternalID(context.Background(), "esp-dup-webhook", "bounced", "hard bounce", "evt-webhook-dup", map[string]any{"bounce_type": "hard"})
	if err != nil {
		t.Fatalf("UpsertWebhookStatusByExternalID: %v", err)
	}
	if !inserted {
		t.Fatal("inserted=false want true")
	}

	var newCount, oldCount int
	if err := db.QueryRow(context.Background(), `
select count(*)
from email_status_history
where email_record_id='rec-webhook-new' and status='bounced'`).Scan(&newCount); err != nil {
		t.Fatalf("query new record history: %v", err)
	}
	if err := db.QueryRow(context.Background(), `
select count(*)
from email_status_history
where email_record_id='rec-webhook-old' and status='bounced'`).Scan(&oldCount); err != nil {
		t.Fatalf("query old record history: %v", err)
	}
	if newCount != 1 || oldCount != 0 {
		t.Fatalf("history counts new=%d old=%d want new=1 old=0", newCount, oldCount)
	}
}
