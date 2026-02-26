package rbac

import "fmt"

const (
	TenantRoleOwner  = "tenant_owner"
	TenantRoleAdmin  = "tenant_admin"
	TenantRoleMember = "member"
)

// MapTenantRoleToCasbin maps tenant role names ("owner", "admin", "member")
// to their corresponding Casbin role identifiers. It returns an error if the
// provided role name does not match a supported tenant role.
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
