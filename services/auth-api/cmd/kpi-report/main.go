package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	DBDSN          string
	Format         string
	MixpanelExport string
}

type report struct {
	GeneratedAt     time.Time        `json:"generated_at"`
	Database        *databaseSummary `json:"database,omitempty"`
	Mixpanel        *mixpanelSummary `json:"mixpanel,omitempty"`
	Notes           []string         `json:"notes,omitempty"`
	Recommendations []string         `json:"recommendations,omitempty"`
}

type databaseSummary struct {
	CohortStart                 *time.Time     `json:"cohort_start,omitempty"`
	CohortEnd                   *time.Time     `json:"cohort_end,omitempty"`
	RegisteredUsers             int            `json:"registered_users"`
	VerifiedUsers               int            `json:"verified_users"`
	ActivationRatePct           *float64       `json:"activation_rate_pct,omitempty"`
	MedianTTVSeconds            *float64       `json:"median_ttv_seconds,omitempty"`
	MedianQueuedToSentSeconds   *float64       `json:"median_queued_to_sent_seconds,omitempty"`
	UnverifiedUsersWithoutSent  int            `json:"unverified_users_without_sent"`
	UnverifiedUsersWithoutClick int            `json:"unverified_users_without_click"`
	UsersWithOTPErrors          int            `json:"users_with_otp_errors"`
	UsersWithBouncedEmail       int            `json:"users_with_bounced_email"`
	UsersWithFailedEmail        int            `json:"users_with_failed_email"`
	ActivatedByMethod           map[string]int `json:"activated_by_method,omitempty"`
}

type mixpanelSummary struct {
	Path          string         `json:"path"`
	TotalEvents   int            `json:"total_events"`
	RelevantCount map[string]int `json:"relevant_event_counts,omitempty"`
}

func main() {
	cfg := loadConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rep, err := buildReport(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kpi-report: %v\n", err)
		os.Exit(1)
	}

	var output []byte
	switch cfg.Format {
	case "json":
		output, err = json.MarshalIndent(rep, "", "  ")
		if err == nil {
			output = append(output, '\n')
		}
	case "markdown":
		output = []byte(renderMarkdown(rep))
	default:
		err = fmt.Errorf("unsupported format %q", cfg.Format)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "kpi-report: %v\n", err)
		os.Exit(1)
	}

	if _, err := os.Stdout.Write(output); err != nil {
		fmt.Fprintf(os.Stderr, "kpi-report: write output: %v\n", err)
		os.Exit(1)
	}
}

func loadConfig() config {
	cfg := config{}
	flag.StringVar(&cfg.DBDSN, "db-dsn", strings.TrimSpace(os.Getenv("DB_DSN")), "PostgreSQL DSN for the auth database")
	flag.StringVar(&cfg.Format, "format", "markdown", "Output format: markdown or json")
	flag.StringVar(&cfg.MixpanelExport, "mixpanel-export", "", "Optional path to a Mixpanel/Segment export file (.json, .ndjson, .csv)")
	flag.Parse()

	cfg.DBDSN = strings.TrimSpace(cfg.DBDSN)
	cfg.Format = strings.ToLower(strings.TrimSpace(cfg.Format))
	cfg.MixpanelExport = strings.TrimSpace(cfg.MixpanelExport)
	return cfg
}

func buildReport(ctx context.Context, cfg config) (*report, error) {
	rep := &report{GeneratedAt: time.Now().UTC()}

	dbDSN := cfg.DBDSN
	if dbDSN == "" {
		rep.Notes = append(rep.Notes, "DB_DSN is not set; database-backed KPI metrics were not collected.")
	} else {
		db, err := pgxpool.New(ctx, dbDSN)
		if err != nil {
			return nil, fmt.Errorf("connect database: %w", err)
		}
		defer db.Close()
		if err := db.Ping(ctx); err != nil {
			return nil, fmt.Errorf("ping database: %w", err)
		}
		summary, err := loadDatabaseSummary(ctx, db)
		if err != nil {
			return nil, fmt.Errorf("load database summary: %w", err)
		}
		rep.Database = summary
		if summary.RegisteredUsers == 0 {
			rep.Notes = append(rep.Notes, "No registration cohort rows were found in the connected database.")
		}
	}

	if cfg.MixpanelExport == "" {
		rep.Notes = append(rep.Notes, "No Mixpanel/Segment export file was provided.")
	} else {
		summary, err := loadMixpanelSummary(cfg.MixpanelExport)
		if err != nil {
			return nil, fmt.Errorf("load Mixpanel export %q: %w", cfg.MixpanelExport, err)
		}
		rep.Mixpanel = summary
	}

	rep.Recommendations = deriveRecommendations(rep)
	if rep.Database == nil && rep.Mixpanel == nil {
		rep.Notes = append(rep.Notes, "At least one data source is required to produce actionable KPI output.")
	}
	return rep, nil
}

func loadDatabaseSummary(ctx context.Context, db *pgxpool.Pool) (*databaseSummary, error) {
	summary := &databaseSummary{ActivatedByMethod: map[string]int{}}
	var cohortStart, cohortEnd sql.NullTime

	if err := db.QueryRow(ctx, `
with registration_cohort as (
  select u.id, u.created_at, u.email_verified_at
  from users u
  where exists (
    select 1
    from email_verifications ev
    where ev.user_id = u.id
  )
)
select
  min(created_at),
  max(created_at),
  count(*),
  count(*) filter (where email_verified_at is not null)
from registration_cohort`).Scan(&cohortStart, &cohortEnd, &summary.RegisteredUsers, &summary.VerifiedUsers); err != nil {
		return nil, err
	}
	if cohortStart.Valid {
		ts := cohortStart.Time.UTC()
		summary.CohortStart = &ts
	}
	if cohortEnd.Valid {
		ts := cohortEnd.Time.UTC()
		summary.CohortEnd = &ts
	}

	if summary.RegisteredUsers > 0 {
		rate := (float64(summary.VerifiedUsers) / float64(summary.RegisteredUsers)) * 100
		summary.ActivationRatePct = &rate
	}

	if value, err := queryOptionalFloat64(ctx, db, `
with registration_cohort as (
  select u.created_at, u.email_verified_at
  from users u
  where exists (
    select 1
    from email_verifications ev
    where ev.user_id = u.id
  )
    and u.email_verified_at is not null
)
select percentile_cont(0.5) within group (order by extract(epoch from (email_verified_at - created_at)))
from registration_cohort`); err != nil {
		return nil, err
	} else {
		summary.MedianTTVSeconds = value
	}

	if value, err := queryOptionalFloat64(ctx, db, `
with per_record as (
  select
    er.created_at as queued_at,
    min(esh.created_at) filter (where esh.status = 'sent') as sent_at
  from email_records er
  left join email_status_history esh on esh.email_record_id = er.id
  where er.template = 'verification_email'
  group by er.id, er.created_at
)
select percentile_cont(0.5) within group (order by extract(epoch from (sent_at - queued_at)))
from per_record
where sent_at is not null`); err != nil {
		return nil, err
	} else {
		summary.MedianQueuedToSentSeconds = value
	}

	counts := []struct {
		target *int
		query  string
	}{
		{
			target: &summary.UnverifiedUsersWithoutSent,
			query: `
with registration_cohort as (
  select u.id, u.email_verified_at
  from users u
  where exists (select 1 from email_verifications ev where ev.user_id = u.id)
)
select count(*)
from registration_cohort rc
where rc.email_verified_at is null
  and exists (
    select 1
    from email_records er
    where er.user_id = rc.id
      and er.template = 'verification_email'
  )
  and not exists (
    select 1
    from email_records er
    join email_status_history esh on esh.email_record_id = er.id
    where er.user_id = rc.id
      and er.template = 'verification_email'
      and esh.status = 'sent'
  )`,
		},
		{
			target: &summary.UnverifiedUsersWithoutClick,
			query: `
with registration_cohort as (
  select u.id, u.email_verified_at
  from users u
  where exists (select 1 from email_verifications ev where ev.user_id = u.id)
)
select count(*)
from registration_cohort rc
where rc.email_verified_at is null
  and exists (
    select 1
    from email_records er
    join email_status_history esh on esh.email_record_id = er.id
    where er.user_id = rc.id
      and er.template = 'verification_email'
      and esh.status = 'sent'
  )
  and not exists (
    select 1
    from email_records er
    join email_status_history esh on esh.email_record_id = er.id
    where er.user_id = rc.id
      and er.template = 'verification_email'
      and esh.status = 'clicked'
  )`,
		},
		{
			target: &summary.UsersWithOTPErrors,
			query: `
select count(distinct user_id)
from email_verifications
where token_type = 'otp'
  and attempts > 0`,
		},
		{
			target: &summary.UsersWithBouncedEmail,
			query: `
select count(distinct er.user_id)
from email_records er
join email_status_history esh on esh.email_record_id = er.id
where er.template = 'verification_email'
  and er.user_id is not null
  and esh.status = 'bounced'`,
		},
		{
			target: &summary.UsersWithFailedEmail,
			query: `
select count(distinct er.user_id)
from email_records er
join email_status_history esh on esh.email_record_id = er.id
where er.template = 'verification_email'
  and er.user_id is not null
  and esh.status = 'failed'`,
		},
	}
	for _, item := range counts {
		if err := db.QueryRow(ctx, item.query).Scan(item.target); err != nil {
			return nil, err
		}
	}

	rows, err := db.Query(ctx, `
select token_type, count(*)
from (
  select distinct on (user_id) user_id, token_type, verified_at
  from email_verifications
  where verified_at is not null
  order by user_id, verified_at asc, id asc
) first_verified
group by token_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			method string
			count  int
		)
		if err := rows.Scan(&method, &count); err != nil {
			return nil, err
		}
		summary.ActivatedByMethod[method] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return summary, nil
}

func queryOptionalFloat64(ctx context.Context, db *pgxpool.Pool, query string) (*float64, error) {
	var value sql.NullFloat64
	if err := db.QueryRow(ctx, query).Scan(&value); err != nil {
		return nil, err
	}
	if !value.Valid {
		return nil, nil
	}
	result := value.Float64
	return &result, nil
}

func loadMixpanelSummary(path string) (*mixpanelSummary, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	summary := &mixpanelSummary{
		Path:          path,
		RelevantCount: map[string]int{},
	}

	relevant := map[string]bool{
		"verification_registration_started": true,
		"verification_email_sent":           true,
		"verification_email_bounced":        true,
		"verification_link_clicked":         true,
		"verification_otp_failed":           true,
		"account_activated":                 true,
	}

	recordEvent := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		summary.TotalEvents++
		if relevant[name] {
			summary.RelevantCount[name]++
		}
	}

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return summary, nil
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		reader := csv.NewReader(bytes.NewReader(raw))
		records, err := reader.ReadAll()
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			return summary, nil
		}
		headerIndex := map[string]int{}
		for idx, name := range records[0] {
			headerIndex[strings.ToLower(strings.TrimSpace(name))] = idx
		}
		eventIdx := -1
		for _, key := range []string{"event", "event_name", "name"} {
			if idx, ok := headerIndex[key]; ok {
				eventIdx = idx
				break
			}
		}
		if eventIdx < 0 {
			return nil, fmt.Errorf("csv export is missing an event column")
		}
		for _, row := range records[1:] {
			if eventIdx < len(row) {
				recordEvent(row[eventIdx])
			}
		}
		return summary, nil
	default:
	}

	if trimmed[0] == '[' {
		var rows []map[string]any
		if err := json.Unmarshal(trimmed, &rows); err != nil {
			return nil, err
		}
		for _, row := range rows {
			recordEvent(extractEventName(row))
		}
		return summary, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var row map[string]any
		if err := decoder.Decode(&row); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		recordEvent(extractEventName(row))
	}
	return summary, nil
}

func extractEventName(row map[string]any) string {
	for _, key := range []string{"event", "event_name", "name"} {
		if value, ok := row[key]; ok {
			if name, ok := value.(string); ok {
				return name
			}
		}
	}
	return ""
}

func deriveRecommendations(rep *report) []string {
	recommendations := make([]string, 0, 5)
	if rep.Database == nil || rep.Database.RegisteredUsers == 0 {
		recommendations = append(recommendations, "Run this report against a populated environment and attach a Mixpanel/Segment export so KPI results can be validated on production-like data.")
	}
	if rep.Database != nil {
		if rep.Database.UnverifiedUsersWithoutSent > 0 || rep.Database.UsersWithFailedEmail > 0 || rep.Database.UsersWithBouncedEmail > 0 {
			recommendations = append(recommendations, "Prioritize email delivery health. This repository now emits `verification_email_sent` with `latency_from_queue_ms`, which makes queued-to-sent delay measurable in exports.")
		}
		if rep.Database.UnverifiedUsersWithoutClick > 0 {
			recommendations = append(recommendations, "Reduce sent-but-not-clicked drop-off with clearer verification email copy, explicit same-device OTP fallback, and resend guidance. Those copy changes are included in this PR.")
		}
		if rep.Database.UsersWithOTPErrors > 0 {
			recommendations = append(recommendations, "Use the new `verification_otp_failed` event to break down invalid, expired, and locked-out OTP attempts, then simplify the OTP UX around the dominant reason.")
		}
		recommendations = append(recommendations, "Use `VERIFICATION_TTL_MIN` to test a shorter expiration window in staging before production rollout instead of changing the 15-minute default blindly.")
	}
	if rep.Mixpanel == nil {
		recommendations = append(recommendations, "Provide a Mixpanel or Segment export on the next run. This issue adds the missing registration-start and OTP-failure events so export-only analyses have a denominator and OTP error signal going forward.")
	}
	return dedupeStrings(recommendations)
}

func renderMarkdown(rep *report) string {
	var b strings.Builder
	b.WriteString("# KPI Report\n\n")
	b.WriteString(fmt.Sprintf("- Generated: %s\n", rep.GeneratedAt.Format(time.RFC3339)))
	b.WriteString("- Report command: `go run ./services/auth-api/cmd/kpi-report`\n")
	if rep.Database != nil {
		b.WriteString("- Database metrics: available\n")
	} else {
		b.WriteString("- Database metrics: unavailable\n")
	}
	if rep.Mixpanel != nil {
		b.WriteString(fmt.Sprintf("- Mixpanel export: `%s`\n", rep.Mixpanel.Path))
	} else {
		b.WriteString("- Mixpanel export: unavailable\n")
	}

	b.WriteString("\n## KPI Results\n\n")
	if rep.Database == nil {
		b.WriteString("- Registered users requiring verification: N/A\n")
		b.WriteString("- Verified users: N/A\n")
		b.WriteString("- Activation rate: N/A\n")
		b.WriteString("- Median TTV: N/A\n")
	} else {
		b.WriteString(fmt.Sprintf("- Registered users requiring verification: %d\n", rep.Database.RegisteredUsers))
		b.WriteString(fmt.Sprintf("- Verified users: %d\n", rep.Database.VerifiedUsers))
		b.WriteString(fmt.Sprintf("- Activation rate: %s\n", formatPercent(rep.Database.ActivationRatePct)))
		b.WriteString(fmt.Sprintf("- Median TTV: %s\n", formatSeconds(rep.Database.MedianTTVSeconds)))
		b.WriteString(fmt.Sprintf("- Median queued-to-sent latency: %s\n", formatSeconds(rep.Database.MedianQueuedToSentSeconds)))
		if rep.Database.CohortStart != nil || rep.Database.CohortEnd != nil {
			b.WriteString(fmt.Sprintf("- Cohort window: %s -> %s\n", formatTime(rep.Database.CohortStart), formatTime(rep.Database.CohortEnd)))
		}
	}

	b.WriteString("\n## Bottlenecks\n\n")
	if rep.Database == nil {
		b.WriteString("- Email delay / send gap: N/A\n")
		b.WriteString("- Sent but not clicked: N/A\n")
		b.WriteString("- OTP input errors: N/A\n")
	} else {
		b.WriteString(fmt.Sprintf("- Unverified users without a `sent` event: %d\n", rep.Database.UnverifiedUsersWithoutSent))
		b.WriteString(fmt.Sprintf("- Unverified users with `sent` but no `clicked` event: %d\n", rep.Database.UnverifiedUsersWithoutClick))
		b.WriteString(fmt.Sprintf("- Users with OTP input errors (`attempts > 0`): %d\n", rep.Database.UsersWithOTPErrors))
		b.WriteString(fmt.Sprintf("- Users with bounced verification email: %d\n", rep.Database.UsersWithBouncedEmail))
		b.WriteString(fmt.Sprintf("- Users with failed verification email: %d\n", rep.Database.UsersWithFailedEmail))
	}

	if rep.Database != nil && len(rep.Database.ActivatedByMethod) > 0 {
		b.WriteString("\n## Activation Methods\n\n")
		methods := sortedKeys(rep.Database.ActivatedByMethod)
		for _, method := range methods {
			b.WriteString(fmt.Sprintf("- %s: %d\n", method, rep.Database.ActivatedByMethod[method]))
		}
	}

	if rep.Mixpanel != nil {
		b.WriteString("\n## Export Counts\n\n")
		b.WriteString(fmt.Sprintf("- Total events read: %d\n", rep.Mixpanel.TotalEvents))
		for _, key := range sortedKeys(rep.Mixpanel.RelevantCount) {
			b.WriteString(fmt.Sprintf("- %s: %d\n", key, rep.Mixpanel.RelevantCount[key]))
		}
	}

	if len(rep.Notes) > 0 {
		b.WriteString("\n## Notes\n\n")
		for _, note := range rep.Notes {
			b.WriteString(fmt.Sprintf("- %s\n", note))
		}
	}

	if len(rep.Recommendations) > 0 {
		b.WriteString("\n## Recommendations\n\n")
		for _, recommendation := range rep.Recommendations {
			b.WriteString(fmt.Sprintf("- %s\n", recommendation))
		}
	}

	return b.String()
}

func formatPercent(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.2f%%", *value)
}

func formatSeconds(value *float64) string {
	if value == nil {
		return "N/A"
	}
	duration := time.Duration(*value * float64(time.Second))
	if duration < time.Minute {
		return duration.Round(time.Second).String()
	}
	return duration.Round(time.Second).String()
}

func formatTime(ts *time.Time) string {
	if ts == nil || ts.IsZero() {
		return "N/A"
	}
	return ts.UTC().Format(time.RFC3339)
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func sortedKeys[V any](items map[string]V) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
