package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ DB *pgxpool.Pool }

type MemberDTO struct {
	UserID    string
	Email     string
	Role      string
	CreatedAt time.Time
}

func (s *Store) TenantUserRole(ctx context.Context, tenantID, userID string) (string, bool, error) {
	var role string
	err := s.DB.QueryRow(ctx, `select role from tenant_users where tenant_id=$1 and user_id=$2`, tenantID, userID).Scan(&role)
	if err == nil {
		return role, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return "", false, err
}

func (s *Store) ListMembers(ctx context.Context, tenantID string) ([]MemberDTO, error) {
	rows, err := s.DB.Query(ctx, `
select tu.user_id, coalesce(u.email, ''), tu.role, tu.created_at
from tenant_users tu
join users u on u.id = tu.user_id
where tu.tenant_id = $1
order by tu.created_at asc`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := make([]MemberDTO, 0)
	for rows.Next() {
		var m MemberDTO
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (s *Store) AddMember(ctx context.Context, tenantID, userID, role string) error {
	_, err := s.DB.Exec(ctx, `insert into tenant_users(tenant_id, user_id, role, created_at) values($1, $2, $3, now())`, tenantID, userID, role)
	return err
}

func (s *Store) UpdateMemberRole(ctx context.Context, tenantID, userID, role string) (bool, error) {
	cmd, err := s.DB.Exec(ctx, `update tenant_users set role = $3 where tenant_id = $1 and user_id = $2`, tenantID, userID, role)
	if err != nil {
		return false, err
	}
	return cmd.RowsAffected() > 0, nil
}

func (s *Store) RemoveMember(ctx context.Context, tenantID, userID string) (bool, error) {
	cmd, err := s.DB.Exec(ctx, `delete from tenant_users where tenant_id = $1 and user_id = $2`, tenantID, userID)
	if err != nil {
		return false, err
	}
	return cmd.RowsAffected() > 0, nil
}

func (s *Store) UserExists(ctx context.Context, userID string) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx, `select exists(select 1 from users where id = $1)`, userID).Scan(&ok)
	return ok, err
}

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
