# Deployment

## Docker Compose

```bash
make init
```

This starts postgres, redis, auth-api, admin-api, email-worker, Mailpit, Prometheus, Alertmanager, Grafana, and runs migration.

## Smoke test

```bash
make smoke
```

The smoke script bootstraps a tenant/user and calls admin endpoint with issued token.

## Monitoring

Monitoring and alerting setup for the email pipeline is documented in [docs/monitoring.md](monitoring.md).
