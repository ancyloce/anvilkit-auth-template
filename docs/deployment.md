# Deployment

## Docker Compose

```bash
make init
```

This starts postgres, redis, auth-api, admin-api and runs migration.

## Smoke test

```bash
make smoke
```

The smoke script bootstraps a tenant/user and calls admin endpoint with issued token.
