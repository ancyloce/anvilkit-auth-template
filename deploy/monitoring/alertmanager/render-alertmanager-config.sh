#!/bin/sh
set -eu

output_path="${1:-/tmp/alertmanager.yml}"

yaml_squote() {
  escaped="$(printf "%s" "$1" | sed "s/'/''/g")"
  printf "'%s'" "$escaped"
}

normalize_bool() {
  value="$(printf "%s" "${1:-false}" | tr '[:upper:]' '[:lower:]')"
  case "$value" in
    true|false)
      printf "%s" "$value"
      ;;
    *)
      echo "ALERT_SMTP_REQUIRE_TLS must be true or false" >&2
      exit 1
      ;;
  esac
}

alert_email_to="${ALERT_EMAIL_TO:-alerts@example.com}"
alert_email_from="${ALERT_EMAIL_FROM:-alerts@anvilkit.local}"
alert_smtp_smarthost="${ALERT_SMTP_SMARTHOST:-mailpit:1025}"
alert_smtp_auth_username="${ALERT_SMTP_AUTH_USERNAME:-}"
alert_smtp_auth_password="${ALERT_SMTP_AUTH_PASSWORD:-}"
alert_smtp_require_tls="$(normalize_bool "${ALERT_SMTP_REQUIRE_TLS:-false}")"

cat > "$output_path" <<EOF
global:
  smtp_smarthost: $(yaml_squote "$alert_smtp_smarthost")
  smtp_from: $(yaml_squote "$alert_email_from")
  smtp_auth_username: $(yaml_squote "$alert_smtp_auth_username")
  smtp_auth_password: $(yaml_squote "$alert_smtp_auth_password")
  smtp_require_tls: $alert_smtp_require_tls

route:
  receiver: email-notifications
  group_by: ['alertname', 'service']
  group_wait: 10s
  group_interval: 30s
  repeat_interval: 1h

receivers:
  - name: email-notifications
    email_configs:
      - to: $(yaml_squote "$alert_email_to")
        send_resolved: true
EOF
