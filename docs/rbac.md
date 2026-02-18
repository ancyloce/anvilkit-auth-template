# RBAC

`admin-api` uses Casbin with file model/policy and runtime role checks from DB.

- Domain string: `tenant:<tenantId>`
- Object: `c.FullPath()`
- Subject: each role from `user_roles`

Policy highlights:

- `tenant_admin` can access `/api/v1/admin/*` on `tenant:*`.
- `org_admin` can access `/api/v1/admin/org/*` on `tenant:*`.
