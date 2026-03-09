package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNilDB               = errors.New("nil_db")
	ErrEmailRecordNotFound = errors.New("email_record_not_found")
	ErrEmptyRecordID       = errors.New("empty_record_id")
	ErrEmptyExternalID     = errors.New("empty_external_id")
	ErrEmptyStatus         = errors.New("empty_status")
	ErrEmptyEmail          = errors.New("empty_email")
)

type Store struct {
	DB *pgxpool.Pool
}

type AnalyticsRecord struct {
	UserID string
	Email  string
	SentAt *time.Time
}

func (s *Store) MarkSent(ctx context.Context, recordID, externalID string) error {
	if s.DB == nil {
		return ErrNilDB
	}
	if strings.TrimSpace(recordID) == "" {
		return ErrEmptyRecordID
	}
	if strings.TrimSpace(externalID) == "" {
		return ErrEmptyExternalID
	}

	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ct, err := tx.Exec(ctx, `update email_records set external_id=$2, status='sent', updated_at=now() where id=$1`, recordID, externalID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrEmailRecordNotFound
	}

	if _, err := tx.Exec(ctx, `insert into email_status_history(id,email_record_id,status,message,meta,created_at) values($1,$2,$3,$4,null,now())`, uuid.NewString(), recordID, "sent", "email sent successfully"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) MarkFailed(ctx context.Context, recordID, reason string) error {
	if s.DB == nil {
		return ErrNilDB
	}
	if strings.TrimSpace(recordID) == "" {
		return ErrEmptyRecordID
	}
	if strings.TrimSpace(reason) == "" {
		reason = "email delivery failed"
	}

	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ct, err := tx.Exec(ctx, `update email_records set status='failed', updated_at=now() where id=$1`, recordID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrEmailRecordNotFound
	}

	if _, err := tx.Exec(ctx, `insert into email_status_history(id,email_record_id,status,message,meta,created_at) values($1,$2,$3,$4,null,now())`, uuid.NewString(), recordID, "failed", reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) MarkBounced(ctx context.Context, recordID, reason, bounceType string, smtpCode, retryCount int) error {
	if s.DB == nil {
		return ErrNilDB
	}
	if strings.TrimSpace(recordID) == "" {
		return ErrEmptyRecordID
	}
	if strings.TrimSpace(reason) == "" {
		reason = "email bounced"
	}
	if strings.TrimSpace(bounceType) == "" {
		bounceType = "unknown"
	}

	metaJSON, err := json.Marshal(map[string]any{"bounce_type": bounceType, "smtp_code": smtpCode, "retry_count": retryCount})
	if err != nil {
		return err
	}

	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ct, err := tx.Exec(ctx, `update email_records set status='bounced', updated_at=now() where id=$1`, recordID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrEmailRecordNotFound
	}

	if _, err := tx.Exec(ctx, `insert into email_status_history(id,email_record_id,status,message,meta,created_at) values($1,$2,$3,$4,$5::jsonb,now())`, uuid.NewString(), recordID, "bounced", reason, string(metaJSON)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Blacklist(ctx context.Context, emailAddr, reason string) error {
	if s.DB == nil {
		return ErrNilDB
	}
	emailAddr = strings.TrimSpace(strings.ToLower(emailAddr))
	if emailAddr == "" {
		return ErrEmptyEmail
	}
	if strings.TrimSpace(reason) == "" {
		reason = "hard bounce"
	}
	_, err := s.DB.Exec(ctx, `
insert into email_blacklist(id,email,reason,created_at,updated_at)
values($1,$2,$3,now(),now())
on conflict (email)
do update set reason=excluded.reason, updated_at=now()
`, uuid.NewString(), emailAddr, reason)
	return err
}

func (s *Store) IsBlacklisted(ctx context.Context, emailAddr string) (bool, error) {
	if s.DB == nil {
		return false, ErrNilDB
	}
	emailAddr = strings.TrimSpace(strings.ToLower(emailAddr))
	if emailAddr == "" {
		return false, ErrEmptyEmail
	}
	var exists bool
	if err := s.DB.QueryRow(ctx, `select exists(select 1 from email_blacklist where lower(email)=lower($1))`, emailAddr).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (s *Store) UpsertWebhookStatusByExternalID(ctx context.Context, externalID, status, message, eventID string, meta map[string]any) (bool, error) {
	if s.DB == nil {
		return false, ErrNilDB
	}
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return false, ErrEmptyExternalID
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return false, ErrEmptyStatus
	}

	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var recordID string
	if err := tx.QueryRow(ctx, `
select id
from email_records
where external_id=$1
order by updated_at desc, created_at desc, id desc
limit 1
for update`, externalID).Scan(&recordID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrEmailRecordNotFound
		}
		return false, err
	}

	eventID = strings.TrimSpace(eventID)
	if eventID != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `
select exists(
  select 1
  from email_status_history
  where email_record_id=$1 and status=$2 and meta->>'event_id'=$3
)`, recordID, status, eventID).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			if err := tx.Commit(ctx); err != nil {
				return false, err
			}
			return false, nil
		}
	}

	if meta == nil {
		meta = map[string]any{}
	}
	if eventID != "" {
		meta["event_id"] = eventID
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return false, err
	}

	if _, err := tx.Exec(ctx, `update email_records set status=$2, updated_at=now() where id=$1`, recordID, status); err != nil {
		return false, err
	}

	if _, err := tx.Exec(ctx, `insert into email_status_history(id,email_record_id,status,message,meta,created_at) values($1,$2,$3,$4,$5::jsonb,now())`, uuid.NewString(), recordID, status, message, string(metaJSON)); err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) LookupAnalyticsRecordByID(ctx context.Context, recordID string) (*AnalyticsRecord, error) {
	return s.lookupAnalyticsRecord(ctx, "er.id = $1", strings.TrimSpace(recordID))
}

func (s *Store) LookupAnalyticsRecordByExternalID(ctx context.Context, externalID string) (*AnalyticsRecord, error) {
	return s.lookupAnalyticsRecord(ctx, "er.external_id = $1", strings.TrimSpace(externalID))
}

func (s *Store) lookupAnalyticsRecord(ctx context.Context, predicate string, value string) (*AnalyticsRecord, error) {
	if s.DB == nil {
		return nil, ErrNilDB
	}

	var record AnalyticsRecord
	var userID sql.NullString
	err := s.DB.QueryRow(ctx, `
select
  er.user_id,
  er.to_email,
  (
    select esh.created_at
    from email_status_history esh
    where esh.email_record_id = er.id
      and esh.status = 'sent'
    order by esh.created_at desc
    limit 1
  ) as sent_at
from email_records er
where `+predicate+`
order by er.updated_at desc, er.created_at desc, er.id desc
limit 1`,
		value,
	).Scan(&userID, &record.Email, &record.SentAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEmailRecordNotFound
		}
		return nil, err
	}
	if userID.Valid {
		record.UserID = userID.String
	}
	return &record, nil
}
