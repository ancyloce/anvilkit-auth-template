package store

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNilDB               = errors.New("nil_db")
	ErrEmailRecordNotFound = errors.New("email_record_not_found")
	ErrEmptyRecordID       = errors.New("empty_record_id")
	ErrEmptyExternalID     = errors.New("empty_external_id")
)

type Store struct {
	DB *pgxpool.Pool
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
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	ct, err := tx.Exec(
		ctx,
		`update email_records set external_id=$2, status='sent', updated_at=now() where id=$1`,
		recordID,
		externalID,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrEmailRecordNotFound
	}

	if _, err := tx.Exec(
		ctx,
		`insert into email_status_history(id,email_record_id,status,message,meta,created_at) values($1,$2,$3,$4,null,now())`,
		uuid.NewString(),
		recordID,
		"sent",
		"email sent successfully",
	); err != nil {
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
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	ct, err := tx.Exec(
		ctx,
		`update email_records set status='failed', updated_at=now() where id=$1`,
		recordID,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrEmailRecordNotFound
	}

	if _, err := tx.Exec(
		ctx,
		`insert into email_status_history(id,email_record_id,status,message,meta,created_at) values($1,$2,$3,$4,null,now())`,
		uuid.NewString(),
		recordID,
		"failed",
		reason,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
