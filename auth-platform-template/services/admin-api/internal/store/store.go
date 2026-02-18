package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ DB *pgxpool.Pool }

func (s *Store) RolesForUser(ctx context.Context, tenantID, userID string) ([]string, error) {
	rows, err := s.DB.Query(ctx, `select role from user_roles where tenant_id=$1 and user_id=$2`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := []string{}
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

func (s *Store) AssignRole(ctx context.Context, tenantID, userID, role string) error {
	_, err := s.DB.Exec(ctx, `insert into user_roles(tenant_id,user_id,role,created_at) values($1,$2,$3,now()) on conflict do nothing`, tenantID, userID, role)
	return err
}
