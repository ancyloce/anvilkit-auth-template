# Architecture

This template contains:

- `auth-api`: identity bootstrap/register/login/refresh/logout.
- `admin-api`: tenant-scoped administrative RBAC APIs.
- `modules/common-go`: shared libs (config, db, redis, jwt, middleware, envelope).

Both services use Gin middleware chain:

1. Recovery
2. RequestID
3. Logger
4. CORS
5. ErrorHandler

Data plane:

- PostgreSQL for users/tenants/roles/refresh tokens.
- Redis for fixed-window rate limiting.
