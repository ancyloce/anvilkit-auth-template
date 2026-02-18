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

Go workspace (`go.work`) with four modules:
- `.` — root workspace
- `./modules/common-go` — shared library (JWT, DB, Redis, HTTP utilities)
- `./services/auth-api` — authentication service
- `./services/admin-api` — admin/RBAC service

### Handler-Store Pattern

Both services follow the same layered structure:
```
cmd/<service>/main.go       → wires config, DB, Redis, handlers
internal/config/            → env-based config loading
internal/handler/           → HTTP handlers (Gin), calls store
internal/store/             → SQL queries via pgx
```

Handlers return errors using `apperr` types; the `ErrorHandler` middleware in `common-go/pkg/httpx/ginmid` translates them to the envelope response format.

### Response Envelope

All responses (success and error) use:
```json
{ "request_id": "...", "code": 0, "message": "ok", "data": {} }
```

Non-zero `code` indicates an error. Error codes are defined in `modules/common-go/pkg/httpx/errcode`.

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
| `BCRYPT_COST` | `12` | bcrypt cost (4–31) |
| `LOGIN_FAIL_LIMIT` | `5` | Max failed logins before rate limit |
| `CORS_ALLOW_ORIGINS` | `http://localhost:3000` | Allowed CORS origins |

## Database Migrations

SQL migrations live in `services/auth-api/migrations/` and are applied in order by `scripts/migrate.sh`. The production compose runs a dedicated `migrate` service before the APIs start.

Key tables: `tenants`, `users`, `tenant_users`, `user_roles`, `user_password_credentials`, `refresh_sessions`.

## Deployment

- **Dev**: `deploy/docker-compose.yml` — builds from source
- **Prod**: `deploy/docker-compose.prod.yml` — pulls images from GHCR, includes memory limits and health checks
- **CI/CD**: GitHub Actions (`ci.yml` for tests, `deploy.yml` for build+push+SSH deploy with automatic rollback)
- Manual deploy: `scripts/remote_deploy.sh`
