# anvilkit-auth-template

Production-ready starter template for a multi-tenant auth platform built with two independent microservices:

- `auth-api` — user authentication and token lifecycle (port 8080)
- `admin-api` — tenant-scoped RBAC admin APIs (port 8081)

**Stack:** Go 1.22, Gin, PostgreSQL 16, Redis 7

## Quick Start

```bash
cp .env.example .env
# Set JWT_ISSUER, JWT_AUDIENCE, JWT_SECRET in .env
make init    # starts containers + runs migrations
make smoke   # verify everything is working
```

## Highlights

- JWT access tokens + refresh token rotation with hashed persistence
- Casbin RBAC with domain-scoped roles for admin APIs
- Unified JSON envelope response with stable error codes and request ID
- Middleware-driven centralized error handling
- Redis fixed-window rate limiting on auth endpoints
- Docker Compose one-command bootstrap; production compose with auto-rollback CI/CD

See `docs/` for architecture and API details.

## Bootstrap API

Bootstrap is implemented in **auth-api** as `POST /v1/bootstrap` (outside `/v1/auth`).

Request body:

```json
{
  "tenant_name": "Acme",
  "owner_email": "owner@example.com",
  "owner_password": "Passw0rd!"
}
```

Response `data` returns `tenant` and `owner_user` in unified Envelope format.

## Configuration

`auth-api` loads config from environment variables on startup. Missing required variables cause an immediate exit (no sensitive values are logged).

| Variable | Required | Default | Description |
|---|---|---|---|
| `JWT_ISSUER` | yes | — | JWT `iss` claim |
| `JWT_AUDIENCE` | yes | — | JWT `aud` claim |
| `JWT_SECRET` | yes | — | JWT signing key |
| `DB_DSN` | no | `postgres://postgres:postgres@localhost:5432/auth?sslmode=disable` | PostgreSQL DSN |
| `REDIS_ADDR` | no | `localhost:6379` | Redis address |
| `ACCESS_TTL_MIN` | no | `15` | Access token TTL (minutes) |
| `REFRESH_TTL_HOURS` | no | `168` | Refresh token TTL (hours, default 7 days) |
| `PASSWORD_MIN_LEN` | no | `8` | Minimum password length |
| `BCRYPT_COST` | no | `12` | bcrypt cost factor (4–31) |
| `LOGIN_FAIL_LIMIT` | no | `5` | Failed login rate limit threshold |
| `LOGIN_FAIL_WINDOW_MIN` | no | `10` | Failed login rate limit window (minutes) |
| `CORS_ALLOW_ORIGINS` | no | `http://localhost:3000` | Allowed CORS origins |
| `CORS_ALLOW_CREDENTIALS` | no | `false` | CORS credentials flag |
| `RBAC_DIR` | no | `internal/rbac` | Casbin config directory (admin-api only) |

## Database Migrations (auth-api)

`services/auth-api/migrations/*.sql` is applied in lexical order (001 → 002 → 003).

### Multi-tenant tables

- `tenants`: tenant metadata (`id`, `name`, optional `slug`, `status`, timestamps).
- `tenant_users`: user-to-tenant membership table with composite primary key `(tenant_id, user_id)`, `role` (`owner`/`admin`/`member`), and `created_at`.
- A single `user` can join multiple `tenant`s (many-to-many relationship via `tenant_users`).

## Deployment (Docker Compose + GitHub Actions)

`.github/workflows/deploy.yml` builds and pushes images to GHCR, then deploys to a Linux server via SSH.

### 1. Server setup

1. Install Docker with the Compose plugin (`docker compose version` should work).
2. Create a deploy directory, e.g. `/opt/anvilkit-auth-template`.
3. Place a `.env` file there with at minimum: `DB_DSN`, `REDIS_ADDR`, `JWT_ISSUER`, `JWT_AUDIENCE`, `JWT_SECRET`, `CORS_ALLOW_ORIGINS`.
4. If using external DB/Redis, point `DB_DSN`/`REDIS_ADDR` to those addresses and set `USE_INTERNAL_DEPS=false` in GitHub Environment Variables.
5. If images are private, run `docker login ghcr.io` on the server first.

Production deploys use `deploy/docker-compose.prod.yml`, which runs a one-shot `migrate` service before starting the API containers.

### 2. GitHub Secrets & Variables

Create a `production` environment (optionally `staging`) in your GitHub repo and configure:

**Required secrets:**
- `DEPLOY_SSH_KEY` — private key for the deploy user
- `DEPLOY_HOST` — server address
- `DEPLOY_USER` — SSH user
- `DEPLOY_PATH` — remote deploy directory
- `JWT_ISSUER`, `JWT_AUDIENCE`, `JWT_SECRET`

**Optional variables:**
- `DEPLOY_PORT` — SSH port (default: 22)
- `USE_INTERNAL_DEPS` — `true` (default, use compose-managed pg/redis) or `false` (external)

### Branch protection recommendation

To prevent accidental deploys with broken code, configure branch protection on `main`:

1. Go to **Settings → Branches → Add branch protection rule**.
2. Enable **Require status checks to pass before merging**.
3. Select status checks `ci / test` and `ci / lint`.

### 3. Triggering a deploy

- **Manual:** `workflow_dispatch` with environment selection (`production` / `staging`)
- **Automatic:** push a `v*` tag → deploys to `production`

Deploy steps:
1. Build and push images to GHCR: `ghcr.io/<owner>/anvilkit-auth-template-{auth,admin}-api:<tag>`
2. Upload `docker-compose.prod.yml`, `remote_deploy.sh`, and migration SQL to the server
3. Run `docker compose run --rm migrate` then `docker compose up -d`
4. Health check both services; auto-rollback to previous tag on failure

### 4. Rollback

`remote_deploy.sh` maintains `current_tag` and `prev_tag` in `${DEPLOY_PATH}/.deploy_state`. On health check failure it automatically re-deploys the previous tag.

### 5. Observability

```bash
docker compose -f deploy/docker-compose.prod.yml --env-file .env ps
docker compose -f deploy/docker-compose.prod.yml --env-file .env logs -f auth-api
docker compose -f deploy/docker-compose.prod.yml --env-file .env logs -f admin-api
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8081/healthz
```

**Common issues:**
- Image pull 401/403 — log in to GHCR on the server or make images public
- Migration failure — check `DB_DSN` connectivity and permissions in `.env`
- Health check failure — check service logs; confirm DB, Redis, and all required JWT env vars are set
