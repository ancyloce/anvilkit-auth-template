# Monitoring and Alerting

This repository uses Prometheus, Grafana, and Alertmanager to monitor the `email-worker` pipeline.

## Metrics

`email-worker` exports the following Prometheus metrics on `GET /metrics`:

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `email_worker_send_attempts_total` | counter | `result=success|failure` | Count of outbound email send attempts. Use this to derive failure rate. |
| `email_worker_send_latency_seconds` | histogram | `result=success|failure` | End-to-end SMTP send latency per attempt. Use `sum/count` for average latency. |
| `email_worker_queue_backlog` | gauge | `queue` | Current Redis queue length for the email queue. |
| `email_worker_queue_backlog_poll_failures_total` | counter | `queue` | Number of failed backlog polls. Useful for diagnosing Redis/metrics gaps. |

The metrics design keeps labels low-cardinality and does not attach per-recipient or per-record identifiers.

## `/metrics` exposure

`email-worker` already serves HTTP traffic on `EMAIL_WEBHOOK_ADDR` for webhook callbacks and health checks. Monitoring extends the same listener:

- `GET /healthz`
- `GET /metrics`
- `POST /webhooks/email-status`

In local Docker Compose, the worker listens on port `8082`, so metrics are reachable at:

```bash
curl -fsS http://localhost:8082/metrics
```

## Queue backlog collection

The worker polls Redis `LLEN` for `EMAIL_QUEUE_NAME` on the interval configured by:

```bash
EMAIL_QUEUE_BACKLOG_POLL_SEC=15
```

This updates `email_worker_queue_backlog`.

## Prometheus

Prometheus is configured in [deploy/monitoring/prometheus/prometheus.yml](/root/Rhett/anvilkit-auth-template/deploy/monitoring/prometheus/prometheus.yml) and scrapes:

- `email-worker:8082`

Alert rules live in [deploy/monitoring/prometheus/alerts/email-worker.rules.yml](/root/Rhett/anvilkit-auth-template/deploy/monitoring/prometheus/alerts/email-worker.rules.yml).

## Grafana

Grafana provisioning is version-controlled:

- datasource: [deploy/monitoring/grafana/provisioning/datasources/prometheus.yml](/root/Rhett/anvilkit-auth-template/deploy/monitoring/grafana/provisioning/datasources/prometheus.yml)
- dashboard provider: [deploy/monitoring/grafana/provisioning/dashboards/dashboards.yml](/root/Rhett/anvilkit-auth-template/deploy/monitoring/grafana/provisioning/dashboards/dashboards.yml)
- dashboard JSON: [deploy/monitoring/grafana/dashboards/email-worker-overview.json](/root/Rhett/anvilkit-auth-template/deploy/monitoring/grafana/dashboards/email-worker-overview.json)

The dashboard includes:

- queue backlog
- email send failure rate
- average send latency
- success/failure throughput trends

Local Grafana URL:

```bash
http://localhost:3001
```

Default local credentials come from `.env` / `.env.example`:

```bash
GRAFANA_ADMIN_USER=admin
GRAFANA_ADMIN_PASSWORD=admin
```

## Alert rules

Two alert rules are configured:

1. `EmailWorkerHighSendFailureRate`
   - fires when failure rate is greater than `5%`
   - expression uses `email_worker_send_attempts_total`
2. `EmailWorkerQueueBacklogHigh`
   - fires when queue backlog is greater than `1000`
   - expression uses `email_worker_queue_backlog`

## Alert notifications

Alertmanager is configured from the template [deploy/monitoring/alertmanager/alertmanager.yml.tmpl](/root/Rhett/anvilkit-auth-template/deploy/monitoring/alertmanager/alertmanager.yml.tmpl), which is rendered at container start from environment variables.

Notifications are delivered by email. The receiver is configured with environment variables so credentials are not hardcoded:

```bash
ALERT_EMAIL_TO=alerts@example.com
ALERT_EMAIL_FROM=alerts@anvilkit.local
ALERT_SMTP_SMARTHOST=mailpit:1025
ALERT_SMTP_AUTH_USERNAME=
ALERT_SMTP_AUTH_PASSWORD=
ALERT_SMTP_REQUIRE_TLS=false
```

Local development uses `Mailpit` as the SMTP sink and inbox viewer:

- SMTP: `localhost:1025`
- UI/API: `http://localhost:8025`

Production/staging can point the same variables to a real SMTP relay.

## Local setup

Start the stack:

```bash
cp .env.example .env
docker compose -f deploy/docker-compose.yml up -d --build
```

Check the main services:

```bash
curl -fsS http://localhost:8082/healthz
curl -fsS http://localhost:8082/metrics | rg 'email_worker_(send_attempts_total|send_latency_seconds|queue_backlog)'
```

## Local alert testing

### 1. Trigger queue backlog alert

Populate the Redis list beyond the threshold:

```bash
python - <<'PY'
import json
for i in range(1001):
    print(json.dumps({
        "record_id": f"load-{i}",
        "to": "user@example.com",
        "subject": "test",
        "html_body": "<p>test</p>",
        "text_body": "test"
    }))
PY
```

Then enqueue the generated payloads into Redis:

```bash
python - <<'PY' | while IFS= read -r payload; do
  docker exec anvilkit-redis redis-cli RPUSH email:send "$payload" >/dev/null
done
import json
for i in range(1001):
    print(json.dumps({
        "record_id": f"load-{i}",
        "to": "user@example.com",
        "subject": "test",
        "html_body": "<p>test</p>",
        "text_body": "test"
    }))
PY
```

After Prometheus evaluates the rule and Alertmanager sends the notification, confirm delivery in Mailpit:

```bash
curl -fsS http://localhost:8025/api/v1/messages
```

### 2. Trigger send failure-rate alert

Restart only `email-worker` with an invalid SMTP host so sends fail while Alertmanager still uses Mailpit:

```bash
docker compose -f deploy/docker-compose.yml stop email-worker
docker compose -f deploy/docker-compose.yml run -d --name anvilkit-email-worker-fail --service-ports -e SMTP_HOST=invalid-smtp-host email-worker
```

Enqueue several jobs, wait for the alert to fire, then inspect Mailpit again:

```bash
python - <<'PY' | while IFS= read -r payload; do
  docker exec anvilkit-redis redis-cli RPUSH email:send "$payload" >/dev/null
done
import json
for i in range(20):
    print(json.dumps({
        "record_id": f"fail-{i}",
        "to": "user@example.com",
        "subject": "test",
        "html_body": "<p>test</p>",
        "text_body": "test"
    }))
PY
curl -fsS http://localhost:8025/api/v1/messages
```

When finished:

```bash
docker rm -f anvilkit-email-worker-fail
docker compose -f deploy/docker-compose.yml up -d email-worker
```

## Validation

Useful local validation commands:

```bash
go test ./services/email-worker/... -count=1
docker run --rm --entrypoint promtool -v "$PWD/deploy/monitoring/prometheus/alerts:/rules:ro" prom/prometheus:v3.5.0 check rules /rules/email-worker.rules.yml
docker run --rm --entrypoint sh -v "$PWD/deploy/monitoring/alertmanager:/config:ro" -e ALERT_EMAIL_TO=alerts@example.com -e ALERT_EMAIL_FROM=alerts@example.com -e ALERT_SMTP_SMARTHOST=mailpit:1025 -e ALERT_SMTP_AUTH_USERNAME= -e ALERT_SMTP_AUTH_PASSWORD= -e ALERT_SMTP_REQUIRE_TLS=false prom/alertmanager:v0.28.1 -ec 'sed -e "s|__ALERT_EMAIL_TO__|${ALERT_EMAIL_TO}|g" -e "s|__ALERT_EMAIL_FROM__|${ALERT_EMAIL_FROM}|g" -e "s|__ALERT_SMTP_SMARTHOST__|${ALERT_SMTP_SMARTHOST}|g" -e "s|__ALERT_SMTP_AUTH_USERNAME__|${ALERT_SMTP_AUTH_USERNAME}|g" -e "s|__ALERT_SMTP_AUTH_PASSWORD__|${ALERT_SMTP_AUTH_PASSWORD}|g" -e "s|__ALERT_SMTP_REQUIRE_TLS__|${ALERT_SMTP_REQUIRE_TLS}|g" /config/alertmanager.yml.tmpl > /tmp/alertmanager.yml && alertmanager --config.file=/tmp/alertmanager.yml --log.level=error --cluster.listen-address= >/tmp/alertmanager.log 2>&1 & pid=$!; sleep 2; kill $pid; wait $pid || test $? -eq 143'
```
