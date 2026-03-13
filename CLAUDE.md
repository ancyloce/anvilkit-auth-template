# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Multi-tenant authentication platform with three independent Go microservices sharing a PostgreSQL + Redis backend. Built as a production-ready starter template.

- **auth-api** (port 8080) — user authentication: register, login, token refresh/revoke, email verification
- **admin-api** (port 8081) — tenant RBAC: role assignment and lookup via Casbin
- **email-worker** (port 8082 webhook, 9090 metrics) — async email delivery, bounce handling, Prometheus metrics

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

Go workspace (`go.work`) with six modules:
- `.` — root workspace
- `./modules/common-go` — shared library (JWT, DB, Redis, HTTP utilities, queue)
- `./services/auth-api` — authentication service
- `./services/admin-api` — admin/RBAC service
- `./services/email-worker` — async email delivery worker
- `./tools/healthcheck` — health check utility

### Handler-Store Pattern

Both HTTP services follow the same layered structure:
```
cmd/<service>/main.go       → wires config, DB, Redis, handlers
internal/config/            → env-based config loading
internal/handler/           → HTTP handlers (Gin), calls store
internal/store/             → SQL queries via pgx
internal/rbac/              → Casbin RBAC config (admin-api only)
```

Additional auth-api internal packages:
```
internal/auth/crypto/       → password hashing utilities
internal/auth/token/        → token generation and validation
```

email-worker internal packages:
```
internal/consumer/          → Redis queue consumer loop
internal/sender/            → SMTP send + bounce classification
internal/store/             → email status SQL updates
internal/webhook/           → ESP webhook HTTP server (:8082)
internal/monitoring/        → Prometheus metrics + queue backlog collector (:9090)
internal/config/            → env-based config loading
templates/                  → email HTML/text templates
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

### Middleware Chain (both HTTP services)

Recovery → RequestID → Logger → CORS → ErrorHandler

admin-api adds after ErrorHandler: AuthN (JWT) → AdminRBAC (Casbin)

### Auth Flow

1. JWT access tokens (default 15 min, HS256, signed with `JWT_SECRET`)
2. Refresh tokens stored hashed in `refresh_sessions` table; rotation invalidates the previous token
3. Rate limiting via Redis fixed-window counters on login/register/bootstrap endpoints
4. On register/bootstrap, an `emailSendJob` is JSON-serialized and RPUSH'd to the `email:send` Redis queue
5. `email-worker` BLPOP's jobs, sends via SMTP, and writes delivery status to `email_status_history`
6. User verifies via 6-digit OTP or magic link; `users.email_verified_at` is set and `users.status` → 1

### RBAC (admin-api)

Casbin with domain-scoped model (`internal/rbac/model.conf`, `policy.csv`). Roles are `tenant_admin` / `org_admin`, scoped to `tenant:<id>` domains.

### auth-api Endpoints

| Method | Route | Rate Limit | Auth | Purpose |
|--------|-------|-----------|------|---------|
| GET | `/healthz` | — | No | Health check |
| POST | `/api/v1/bootstrap` | 10/min | No | Create tenant + owner user |
| POST | `/api/v1/auth/register` | 20/min | No | Register user, enqueue verification email |
| POST | `/api/v1/auth/resend-verification` | 15/min | No | Resend verification email |
| POST | `/api/v1/auth/verify-email` | 60/min | No | Verify email with OTP |
| GET | `/api/v1/auth/verify-magic-link` | 60/min | No | Verify email with magic link |
| POST | `/api/v1/auth/login` | 30/min | No | Login, return access + refresh tokens |
| POST | `/api/v1/auth/refresh` | — | No | Rotate refresh token |
| POST | `/api/v1/auth/logout` | — | No | Revoke single refresh token |
| POST | `/api/v1/auth/logout_all` | — | Yes | Revoke all user refresh tokens |
| POST | `/api/v1/auth/switch_tenant` | — | Yes | Issue token for different tenant |

### admin-api Endpoints

| Method | Route | Auth | Purpose |
|--------|-------|------|---------|
| GET | `/healthz` | No | Health check |
| GET | `/api/v1/admin/tenants/:tenantId/me/roles` | JWT+RBAC | Get caller's roles in tenant |
| POST | `/api/v1/admin/tenants/:tenantId/users/:userId/roles/:role` | JWT+RBAC | Assign role to user |
| GET | `/api/v1/admin/tenants/:tenantId/members` | JWT+RBAC | List tenant members |
| POST | `/api/v1/admin/tenants/:tenantId/members` | JWT+RBAC | Add member to tenant |
| PATCH | `/api/v1/admin/tenants/:tenantId/members/:uid` | JWT+RBAC | Update member role |
| DELETE | `/api/v1/admin/tenants/:tenantId/members/:uid` | JWT+RBAC | Remove member from tenant |

## Environment Variables

Copy `.env.example` to `.env`. Required at startup (auth-api will fail without these):

| Variable | Description |
|---|---|
| `JWT_ISSUER` | JWT issuer claim |
| `JWT_AUDIENCE` | JWT audience claim |
| `JWT_SECRET` | JWT signing key |

Key optional variables for auth-api (all have defaults):

| Variable | Default | Description |
|---|---|---|
| `DB_DSN` | `postgres://postgres:postgres@localhost:5432/auth?sslmode=disable` | PostgreSQL DSN |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `ACCESS_TTL_MIN` | `15` | Access token TTL (minutes) |
| `REFRESH_TTL_HOURS` | `168` | Refresh token TTL (hours) |
| `VERIFICATION_TTL_MIN` | `15` | Verification OTP / magic-link TTL (minutes) |
| `PASSWORD_MIN_LEN` | `8` | Minimum password length |
| `BCRYPT_COST` | `12` | bcrypt cost (4–31) |
| `LOGIN_FAIL_LIMIT` | `5` | Max failed logins before rate limit |
| `LOGIN_FAIL_WINDOW_MIN` | `10` | Failed login rate limit window (minutes) |
| `CORS_ALLOW_ORIGINS` | `http://localhost:3000` | Allowed CORS origins |
| `CORS_ALLOW_CREDENTIALS` | `false` | CORS credentials flag |
| `RBAC_DIR` | `internal/rbac` | Casbin config directory (admin-api only) |

email-worker environment variables:

| Variable | Required | Default | Description |
|---|---|---|---|
| `SMTP_HOST` | yes | — | SMTP server hostname |
| `SMTP_PORT` | yes | — | SMTP server port |
| `SMTP_USERNAME` | yes | — | SMTP auth username |
| `SMTP_PASSWORD` | yes | — | SMTP auth password |
| `SMTP_FROM_EMAIL` | yes | — | Sender email address |
| `SMTP_FROM_NAME` | no | — | Sender display name |
| `EMAIL_QUEUE_NAME` | no | `email:send` | Redis queue name |
| `EMAIL_QUEUE_POP_TIMEOUT_SEC` | no | `5` | BLPOP blocking timeout (seconds) |
| `EMAIL_QUEUE_BACKLOG_POLL_SEC` | no | `15` | Queue length metrics poll interval (seconds) |
| `EMAIL_WEBHOOK_ADDR` | no | `:8082` | Webhook server listen address |
| `EMAIL_METRICS_ADDR` | no | `:9090` | Prometheus metrics listen address |
| `EMAIL_WEBHOOK_SECRET` | yes | — | HMAC secret for webhook signature validation |

## Database Migrations

SQL migrations live in both service directories and are applied in lexical order by `scripts/migrate.sh`:
- `services/auth-api/migrations/` — core auth + email tables
- `services/admin-api/migrations/` — RBAC tables

| File | Description |
|---|---|
| `auth-api/001_init.sql` | Initial schema: tenants, users, tenant_users, user_roles, refresh_tokens |
| `auth-api/002_authn_core.sql` | Auth hardening: user_password_credentials, refresh_sessions |
| `auth-api/003_multitenant.sql` | Tenant hardening: slug, status; tenant_users role constraint |
| `auth-api/004_email_service.sql` | Email tables: email_verifications, email_jobs, email_records, email_status_history |
| `auth-api/005_email_verifications_token_hash_scope.sql` | Scoped token_hash uniqueness for OTP vs magic_link |
| `auth-api/006_email_blacklist.sql` | email_blacklist table for hard-bounce suppression |
| `auth-api/007_email_blacklist_normalization.sql` | Email blacklist normalization |
| `admin-api/001_casbin_rule.sql` | casbin_rule table for RBAC policies |

The production compose runs a dedicated `migrate` service before the APIs start.

Key tables:
- `tenants` — tenant metadata (id, name, slug, status, created_at)
- `users` — user accounts (id, email, phone, status, email_verified_at, timestamps)
- `user_password_credentials` — password hashes (user_id, password_hash, updated_at)
- `tenant_users` — user-to-tenant membership (tenant_id, user_id, role, created_at)
- `user_roles` — tenant-scoped Casbin roles (tenant_id, user_id, role, created_at)
- `refresh_sessions` — refresh token rotation chain (id, user_id, token_hash, user_agent, ip, expires_at, revoked_at, replaced_by, created_at)
- `email_verifications` — hashed OTP + magic-link tokens (user_id, token_hash, token_type, expires_at, verified_at, attempts)
- `email_jobs` — email job envelope (job_type, status, payload)
- `email_records` — per-email send record (job_id, user_id, to_email, external_id, status)
- `email_status_history` — immutable delivery timeline (queued → sent → delivered/bounced/failed)
- `email_blacklist` — suppression list populated on hard bounces
- `casbin_rule` — Casbin RBAC policies (admin-api)

## Deployment

- **Dev**: `deploy/docker-compose.yml` — builds from source
- **Prod**: `deploy/docker-compose.prod.yml` — pulls images from GHCR, includes memory limits and health checks
- **CI/CD**: GitHub Actions (`ci.yml` for tests, `deploy.yml` for build+push+SSH deploy with automatic rollback)
- Manual deploy: `scripts/remote_deploy.sh`
