# anvilkit-auth-template

Open-source style starter template for a multi-tenant auth platform built from day one with two microservices:

- `auth-api` (user auth, token lifecycle)
- `admin-api` (tenant-scoped admin RBAC APIs)

Tech stack: **Go 1.22**, **Gin**, **PostgreSQL**, **Redis**.

## Quick start

```bash
cp .env.example .env
make init
make smoke
```

## Services

- auth-api: `http://localhost:8080`
- admin-api: `http://localhost:8081`

## Highlights

- Unified JSON envelope response with request ID.
- Middleware-driven centralized error handling.
- JWT access + refresh rotation with hashed refresh token persistence.
- Casbin RBAC for admin APIs.
- Docker Compose one-command bootstrap.

See `docs/` for architecture and API details.
