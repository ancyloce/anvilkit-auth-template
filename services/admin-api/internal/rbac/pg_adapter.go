package rbac

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	casbinmodel "github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresAdapter implements the Casbin persist.Adapter interface using PostgreSQL.
type PostgresAdapter struct {
	db *pgxpool.Pool
}

// NewPostgresAdapter creates a new PostgreSQL adapter for Casbin using the provided DSN.
func NewPostgresAdapter(ctx context.Context, dsn string) (*PostgresAdapter, error) {
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err = db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &PostgresAdapter{db: db}, nil
}

func (a *PostgresAdapter) Close() {
	if a == nil || a.db == nil {
		return
	}
	a.db.Close()
}
func (a *PostgresAdapter) LoadPolicy(model casbinmodel.Model) error {
	rows, err := a.db.Query(context.Background(), `SELECT ptype, v0, v1, v2, v3, v4, v5 FROM casbin_rule ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var ptype string
		vals := make([]*string, 6)
		if err = rows.Scan(&ptype, &vals[0], &vals[1], &vals[2], &vals[3], &vals[4], &vals[5]); err != nil {
			return err
		}
		line := ptype
		last := -1
		parts := make([]string, 6)
		for i := range vals {
			if vals[i] != nil {
				parts[i] = *vals[i]
				if parts[i] != "" {
					last = i
				}
			}
		}
		for i := 0; i <= last; i++ {
			line += ", " + parts[i]
		}
		if err = persist.LoadPolicyLine(line, model); err != nil {
			return fmt.Errorf("load policy line: %w", err)
		}
	}
	return rows.Err()
}

func (a *PostgresAdapter) SavePolicy(model casbinmodel.Model) error {
	ctx := context.Background()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackErr := tx.Rollback(ctx)
			if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				log.Printf("rbac: tx rollback failed: %v", rollbackErr)
			}
		}
	}()

	if _, err = tx.Exec(ctx, `TRUNCATE TABLE casbin_rule RESTART IDENTITY`); err != nil {
		return err
	}

	for ptype, ast := range model["p"] {
		for _, rule := range ast.Policy {
			if err = insertRule(ctx, tx, ptype, rule); err != nil {
				return err
			}
		}
	}
	for ptype, ast := range model["g"] {
		for _, rule := range ast.Policy {
			if err = insertRule(ctx, tx, ptype, rule); err != nil {
				return err
			}
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

func (a *PostgresAdapter) AddPolicy(_ string, ptype string, rule []string) error {
	_, err := a.db.Exec(context.Background(),
		`INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4, v5) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		ptype, valueAt(rule, 0), valueAt(rule, 1), valueAt(rule, 2), valueAt(rule, 3), valueAt(rule, 4), valueAt(rule, 5),
	)
	return err
}

func (a *PostgresAdapter) RemovePolicy(_ string, ptype string, rule []string) error {
	query, args := buildPolicyQuery(`DELETE FROM casbin_rule WHERE ptype=$1`, ptype, rule)
	_, err := a.db.Exec(context.Background(), query, args...)
	return err
}

func (a *PostgresAdapter) RemoveFilteredPolicy(_ string, ptype string, fieldIndex int, fieldValues ...string) error {
	base := `DELETE FROM casbin_rule WHERE ptype=$1`
	args := []any{ptype}
	argPos := 2
	for i, v := range fieldValues {
		if v == "" {
			continue
		}
		col := fmt.Sprintf("v%d", fieldIndex+i)
		base += fmt.Sprintf(" AND %s=$%d", col, argPos)
		args = append(args, v)
		argPos++
	}
	_, err := a.db.Exec(context.Background(), base, args...)
	return err
}

func buildPolicyQuery(base, ptype string, rule []string) (string, []any) {
	args := []any{ptype}
	argPos := 2
	for i := 0; i < 6; i++ {
		if i < len(rule) {
			base += fmt.Sprintf(" AND v%d=$%d", i, argPos)
			args = append(args, rule[i])
			argPos++
			continue
		}
		base += fmt.Sprintf(" AND v%d IS NULL", i)
	}
	return base, args
}

func insertRule(ctx context.Context, tx pgx.Tx, ptype string, rule []string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4, v5) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		ptype, valueAt(rule, 0), valueAt(rule, 1), valueAt(rule, 2), valueAt(rule, 3), valueAt(rule, 4), valueAt(rule, 5),
	)
	return err
}

func valueAt(rule []string, idx int) any {
	if idx >= len(rule) {
		return nil
	}
	v := strings.TrimSpace(rule[idx])
	if v == "" {
		return nil
	}
	return v
}

var _ persist.Adapter = (*PostgresAdapter)(nil)
