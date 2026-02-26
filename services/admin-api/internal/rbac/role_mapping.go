package rbac

import "fmt"

const (
	// TenantRoleOwner is the Casbin role identifier used in RBAC policies for a tenant owner.
	TenantRoleOwner = "tenant_owner"
	// TenantRoleAdmin is the Casbin role identifier used in RBAC policies for a tenant administrator.
	TenantRoleAdmin = "tenant_admin"
	// TenantRoleMember is the Casbin role identifier used in RBAC policies for a tenant member.
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
