# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Multi-tenant authentication platform with two independent Go microservices sharing a PostgreSQL + Redis backend. Built as a production-ready starter template.

- **auth-api** (port 8080) — user authentication: register, login, token refresh/revoke
- **admin-api** (port 8081) — tenant RBAC: role assignment and lookup via Casbin

## Common Commands

```bash
make init        # Start containers + run migrations (first-time setup)
make up          # Start all services via docker-compose
make down        # Stop and remove containers
make migrate     # Run SQL migrations only
make smoke       # Run integration/smoke tests
make test        # Run Go unit tests (go test ./... -count=1)
```

Run a single Go test:
```bash
go test ./services/auth-api/... -run TestFunctionName -count=1
```

Lint (uses `.golangci.yml`):
```bash
golangci-lint run ./...
```

## Architecture

### Monorepo Structure

Go workspace (`go.work`) with five modules:
- `.` — root workspace
- `./modules/common-go` — shared library (JWT, DB, Redis, HTTP utilities)
- `./services/auth-api` — authentication service
- `./services/admin-api` — admin/RBAC service
- `./tools/healthcheck` — health check utility

### Handler-Store Pattern

Both services follow the same layered structure:
```
cmd/<service>/main.go       → wires config, DB, Redis, handlers
internal/config/            → env-based config loading (auth-api only)
internal/handler/           → HTTP handlers (Gin), calls store
internal/store/             → SQL queries via pgx
internal/rbac/              → Casbin RBAC config (admin-api only)
```

Additional auth-api internal packages:
```
internal/auth/crypto/       → password hashing utilities
internal/auth/token/        → token generation and validation
```

Handlers return errors using `apperr` types; the `ErrorHandler` middleware in `common-go/pkg/httpx/ginmid` translates them to the envelope response format.

### Response Envelope

All responses (success and error) use:
```json
{ "request_id": "...", "code": 0, "message": "ok", "data": {} }
```

Non-zero `code` indicates an error. Error codes are defined in `modules/common-go/pkg/httpx/errcode`.

### API Prefix Constraint

The current project's API prefix is uniformly constrained to `/api/v1/[module name]`.

For example:

- **auth-api** `/api/v1/auth`
- **admin-api** `/api/v1/admin`

### Middleware Chain (both services)

Recovery → RequestID → Logger → CORS → ErrorHandler

### Auth Flow

1. JWT access tokens (default 15 min, HS256, signed with `JWT_SECRET`)
2. Refresh tokens stored hashed in `refresh_sessions` table; rotation invalidates the previous token
3. Rate limiting via Redis fixed-window counters on login/register/bootstrap endpoints

### RBAC (admin-api)

Casbin with domain-scoped model (`internal/rbac/model.conf`, `policy.csv`). Roles are `tenant_admin` / `org_admin`, scoped to `tenant:<id>` domains.

## Environment Variables

Copy `.env.example` to `.env`. Required at startup (auth-api will fail without these):

| Variable | Description |
|---|---|
| `JWT_ISSUER` | JWT issuer claim |
| `JWT_AUDIENCE` | JWT audience claim |
| `JWT_SECRET` | JWT signing key |

Key optional variables (all have defaults):

| Variable | Default | Description |
|---|---|---|
| `DB_DSN` | `postgres://postgres:postgres@localhost:5432/auth?sslmode=disable` | PostgreSQL DSN |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `ACCESS_TTL_MIN` | `15` | Access token TTL (minutes) |
| `REFRESH_TTL_HOURS` | `168` | Refresh token TTL (hours) |
| `PASSWORD_MIN_LEN` | `8` | Minimum password length |
| `BCRYPT_COST` | `12` | bcrypt cost (4–31) |
| `LOGIN_FAIL_LIMIT` | `5` | Max failed logins before rate limit |
| `LOGIN_FAIL_WINDOW_MIN` | `10` | Failed login rate limit window (minutes) |
| `CORS_ALLOW_ORIGINS` | `http://localhost:3000` | Allowed CORS origins |
| `CORS_ALLOW_CREDENTIALS` | `false` | CORS credentials flag |
| `RBAC_DIR` | `internal/rbac` | Casbin config directory (admin-api only) |

## Database Migrations

SQL migrations live in both service directories and are applied in lexical order by `scripts/migrate.sh`:
- `services/auth-api/migrations/` — core auth tables (001_init.sql, 002_authn_core.sql, 003_multitenant.sql)
- `services/admin-api/migrations/` — RBAC tables (001_casbin_rule.sql)

The production compose runs a dedicated `migrate` service before the APIs start.

Key tables:
- `tenants` — tenant metadata (id, name, created_at)
- `users` — user accounts (id, email, phone, status, timestamps)
- `user_password_credentials` — password hashes (user_id, password_hash, updated_at)
- `tenant_users` — user-to-tenant membership (tenant_id, user_id, created_at)
- `user_roles` — tenant-scoped roles (tenant_id, user_id, role, created_at)
- `refresh_sessions` — refresh token rotation chain (id, user_id, token_hash, user_agent, ip, expires_at, revoked_at, replaced_by, created_at)
- `casbin_rule` — Casbin RBAC policies (admin-api)

## Deployment

- **Dev**: `deploy/docker-compose.yml` — builds from source
- **Prod**: `deploy/docker-compose.prod.yml` — pulls images from GHCR, includes memory limits and health checks
- **CI/CD**: GitHub Actions (`ci.yml` for tests, `deploy.yml` for build+push+SSH deploy with automatic rollback)
- Manual deploy: `scripts/remote_deploy.sh`
