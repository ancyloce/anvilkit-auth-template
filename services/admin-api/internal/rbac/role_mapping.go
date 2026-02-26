package rbac

import "fmt"

const (
	TenantRoleOwner  = "tenant_owner"
	TenantRoleAdmin  = "tenant_admin"
	TenantRoleMember = "member"
)

func MapTenantRoleToCasbin(role string) (string, error) {
	switch role {
	case "owner":
		return TenantRoleOwner, nil
	case "admin":
		return TenantRoleAdmin, nil
	case "member":
		return TenantRoleMember, nil
	default:
		return "", fmt.Errorf("invalid tenant role: %s", role)
	}
}
