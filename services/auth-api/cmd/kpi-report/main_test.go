package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anvilkit-auth-template/services/auth-api/internal/testutil"
)

func TestLoadDatabaseSummary(t *testing.T) {
	db := testutil.MustTestDB(t)
	testutil.TruncateAuthTables(t, db)

	base := time.Date(2026, time.March, 13, 10, 0, 0, 0, time.UTC)
	if _, err := db.Exec(context.Background(), `
insert into users(id,email,status,email_verified_at,created_at,updated_at)
values
  ('user-1','otp-verified@example.com',1,$1,$2,$1),
  ('user-2','otp-errors@example.com',0,null,$3,$3),
  ('user-3','no-send@example.com',0,null,$4,$4),
  ('user-4','magic-verified@example.com',1,$5,$6,$5)`,
		base.Add(2*time.Minute),
		base,
		base.Add(4*time.Minute),
		base.Add(8*time.Minute),
		base.Add(13*time.Minute+30*time.Second),
		base.Add(12*time.Minute),
	); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := db.Exec(context.Background(), `
insert into email_verifications(id,user_id,token_hash,token_type,expires_at,verified_at,attempts,created_at)
values
  ('otp-1','user-1',repeat('a',64),'otp',$1,$2,0,$3),
  ('magic-1','user-1',repeat('b',64),'magic_link',$1,null,0,$3),
  ('otp-2','user-2',repeat('c',64),'otp',$4,null,2,$5),
  ('magic-2','user-2',repeat('d',64),'magic_link',$4,null,0,$5),
  ('otp-3','user-3',repeat('e',64),'otp',$6,null,0,$7),
  ('magic-3','user-3',repeat('f',64),'magic_link',$6,null,0,$7),
  ('otp-4','user-4',repeat('g',64),'otp',$8,null,0,$9),
  ('magic-4','user-4',repeat('h',64),'magic_link',$8,$10,0,$9)`,
		base.Add(15*time.Minute),
		base.Add(2*time.Minute),
		base,
		base.Add(19*time.Minute),
		base.Add(4*time.Minute),
		base.Add(23*time.Minute),
		base.Add(8*time.Minute),
		base.Add(27*time.Minute),
		base.Add(12*time.Minute),
		base.Add(13*time.Minute+30*time.Second),
	); err != nil {
		t.Fatalf("seed email_verifications: %v", err)
	}
	if _, err := db.Exec(context.Background(), `
insert into email_records(id,user_id,to_email,template,subject,status,created_at,updated_at)
values
  ('rec-1','user-1','otp-verified@example.com','verification_email','Verify your email','sent',$1,$1),
  ('rec-2','user-2','otp-errors@example.com','verification_email','Verify your email','sent',$2,$2),
  ('rec-3','user-3','no-send@example.com','verification_email','Verify your email','queued',$3,$3),
  ('rec-4','user-4','magic-verified@example.com','verification_email','Verify your email','sent',$4,$4)`,
		base.Add(10*time.Second),
		base.Add(4*time.Minute+5*time.Second),
		base.Add(8*time.Minute+5*time.Second),
		base.Add(12*time.Minute+5*time.Second),
	); err != nil {
		t.Fatalf("seed email_records: %v", err)
	}
	if _, err := db.Exec(context.Background(), `
insert into email_status_history(id,email_record_id,status,message,meta,created_at)
values
  ('hist-1','rec-1','sent','sent',null,$1),
  ('hist-2','rec-2','sent','sent',null,$2),
  ('hist-3','rec-4','sent','sent',null,$3),
  ('hist-4','rec-4','clicked','clicked',null,$4)`,
		base.Add(40*time.Second),
		base.Add(4*time.Minute+35*time.Second),
		base.Add(12*time.Minute+25*time.Second),
		base.Add(12*time.Minute+45*time.Second),
	); err != nil {
		t.Fatalf("seed email_status_history: %v", err)
	}

	summary, err := loadDatabaseSummary(context.Background(), db)
	if err != nil {
		t.Fatalf("loadDatabaseSummary: %v", err)
	}

	if summary.RegisteredUsers != 4 {
		t.Fatalf("RegisteredUsers=%d want=4", summary.RegisteredUsers)
	}
	if summary.VerifiedUsers != 2 {
		t.Fatalf("VerifiedUsers=%d want=2", summary.VerifiedUsers)
	}
	if summary.ActivationRatePct == nil || *summary.ActivationRatePct != 50 {
		t.Fatalf("ActivationRatePct=%v want=50", summary.ActivationRatePct)
	}
	if summary.MedianTTVSeconds == nil || *summary.MedianTTVSeconds != 105 {
		t.Fatalf("MedianTTVSeconds=%v want=105", summary.MedianTTVSeconds)
	}
	if summary.MedianQueuedToSentSeconds == nil || *summary.MedianQueuedToSentSeconds != 30 {
		t.Fatalf("MedianQueuedToSentSeconds=%v want=30", summary.MedianQueuedToSentSeconds)
	}
	if summary.UnverifiedUsersWithoutSent != 1 {
		t.Fatalf("UnverifiedUsersWithoutSent=%d want=1", summary.UnverifiedUsersWithoutSent)
	}
	if summary.UnverifiedUsersWithoutClick != 1 {
		t.Fatalf("UnverifiedUsersWithoutClick=%d want=1", summary.UnverifiedUsersWithoutClick)
	}
	if summary.UsersWithOTPErrors != 1 {
		t.Fatalf("UsersWithOTPErrors=%d want=1", summary.UsersWithOTPErrors)
	}
	if summary.ActivatedByMethod["otp"] != 1 || summary.ActivatedByMethod["magic_link"] != 1 {
		t.Fatalf("ActivatedByMethod=%+v want otp=1 magic_link=1", summary.ActivatedByMethod)
	}
}

func TestLoadMixpanelSummaryNDJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.ndjson")
	if err := os.WriteFile(path, []byte(stringsJoin(
		`{"event":"verification_registration_started"}`,
		`{"event":"verification_email_sent"}`,
		`{"event":"other_event"}`,
	)), 0o600); err != nil {
		t.Fatalf("write export file: %v", err)
	}

	summary, err := loadMixpanelSummary(path)
	if err != nil {
		t.Fatalf("loadMixpanelSummary: %v", err)
	}
	if summary.TotalEvents != 3 {
		t.Fatalf("TotalEvents=%d want=3", summary.TotalEvents)
	}
	if summary.RelevantCount["verification_registration_started"] != 1 {
		t.Fatalf("verification_registration_started=%d want=1", summary.RelevantCount["verification_registration_started"])
	}
	if summary.RelevantCount["verification_email_sent"] != 1 {
		t.Fatalf("verification_email_sent=%d want=1", summary.RelevantCount["verification_email_sent"])
	}
}

func TestLoadMixpanelSummaryNDJSONAllowsLargeLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large-events.ndjson")

	line, err := json.Marshal(map[string]any{
		"event": "verification_email_sent",
		"properties": map[string]any{
			"payload": strings.Repeat("x", 80*1024),
		},
	})
	if err != nil {
		t.Fatalf("marshal large event: %v", err)
	}
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatalf("write export file: %v", err)
	}

	summary, err := loadMixpanelSummary(path)
	if err != nil {
		t.Fatalf("loadMixpanelSummary: %v", err)
	}
	if summary.TotalEvents != 1 {
		t.Fatalf("TotalEvents=%d want=1", summary.TotalEvents)
	}
	if summary.RelevantCount["verification_email_sent"] != 1 {
		t.Fatalf("verification_email_sent=%d want=1", summary.RelevantCount["verification_email_sent"])
	}
}

func TestLoadMixpanelSummaryCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.csv")
	content := strings.Join([]string{
		"event,distinct_id",
		"verification_registration_started,user-1",
		"verification_email_sent,user-1",
		"other_event,user-2",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write csv export: %v", err)
	}

	summary, err := loadMixpanelSummary(path)
	if err != nil {
		t.Fatalf("loadMixpanelSummary: %v", err)
	}
	if summary.TotalEvents != 3 {
		t.Fatalf("TotalEvents=%d want=3", summary.TotalEvents)
	}
	if summary.RelevantCount["verification_registration_started"] != 1 {
		t.Fatalf("verification_registration_started=%d want=1", summary.RelevantCount["verification_registration_started"])
	}
	if summary.RelevantCount["verification_email_sent"] != 1 {
		t.Fatalf("verification_email_sent=%d want=1", summary.RelevantCount["verification_email_sent"])
	}
}

func TestLoadMixpanelSummaryJSONArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.json")
	payload := []map[string]any{
		{"event": "verification_registration_started"},
		{"event": "verification_email_sent"},
		{"event": "other_event"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal json array: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write json export: %v", err)
	}

	summary, err := loadMixpanelSummary(path)
	if err != nil {
		t.Fatalf("loadMixpanelSummary: %v", err)
	}
	if summary.TotalEvents != 3 {
		t.Fatalf("TotalEvents=%d want=3", summary.TotalEvents)
	}
	if summary.RelevantCount["verification_registration_started"] != 1 {
		t.Fatalf("verification_registration_started=%d want=1", summary.RelevantCount["verification_registration_started"])
	}
	if summary.RelevantCount["verification_email_sent"] != 1 {
		t.Fatalf("verification_email_sent=%d want=1", summary.RelevantCount["verification_email_sent"])
	}
}

func stringsJoin(lines ...string) string {
	return strings.Join(lines, "\n")
}
