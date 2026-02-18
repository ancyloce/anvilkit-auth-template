#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 ]]; then
  echo "usage: $0 <deploy_path> <deploy_tag> <image_owner> [use_internal_deps:true|false]" >&2
  exit 1
fi

DEPLOY_PATH="$1"
DEPLOY_TAG="$2"
IMAGE_OWNER="$3"
USE_INTERNAL_DEPS="${4:-true}"
GHCR_USERNAME="${GHCR_USERNAME:-}"
GHCR_TOKEN="${GHCR_TOKEN:-}"
COMPOSE_PROJECT_NAME="anvilkit-auth"

COMPOSE_FILE="$DEPLOY_PATH/deploy/docker-compose.prod.yml"
STATE_FILE="$DEPLOY_PATH/.deploy_state"
ENV_FILE="$DEPLOY_PATH/.env"
PROJECT_DIRECTORY="$DEPLOY_PATH"
MIGRATIONS_DIR="$DEPLOY_PATH/migrations"

mkdir -p "$DEPLOY_PATH"

if [[ ! -f "$COMPOSE_FILE" ]]; then
  echo "missing $COMPOSE_FILE in $DEPLOY_PATH" >&2
  exit 1
fi

if [[ ! -f "$ENV_FILE" ]]; then
  echo "missing $ENV_FILE in $DEPLOY_PATH; please create it with production secrets" >&2
  exit 1
fi

if [[ -z "$GHCR_USERNAME" || -z "$GHCR_TOKEN" ]]; then
  echo "missing GHCR credentials: GHCR_USERNAME and GHCR_TOKEN are required for pulling private images" >&2
  exit 1
fi

db_dsn="$(awk -F= '/^DB_DSN=/{sub(/^DB_DSN=/,""); print; exit}' "$ENV_FILE")"
if [[ -z "$db_dsn" ]]; then
  echo "missing DB_DSN in $ENV_FILE" >&2
  exit 1
fi

is_local_db="false"
if [[ "$db_dsn" == *"@127.0.0.1:"* || "$db_dsn" == *"@localhost:"* ]]; then
  is_local_db="true"
fi

if [[ "$is_local_db" == "true" ]]; then
  echo "DB_DSN in $ENV_FILE points to localhost/127.0.0.1, which is invalid for container-to-container DB access." >&2
  echo "Use postgres://...@pg:5432/... for internal DB, or an external DB host/IP for external DB." >&2
  exit 1
fi

current_tag=""
prev_tag=""
if [[ -f "$STATE_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$STATE_FILE"
  current_tag="${current_tag:-}"
  prev_tag="${prev_tag:-}"
fi

rollback_tag="${current_tag:-$prev_tag}"

echo "Deploying image tag: $DEPLOY_TAG"
echo "Current tag: ${current_tag:-<none>}"
echo "Previous tag: ${prev_tag:-<none>}"
echo "USE_INTERNAL_DEPS: $USE_INTERNAL_DEPS"

compose_cmd=(
  env
  DEPLOY_PATH="$DEPLOY_PATH"
  MIGRATIONS_DIR="$MIGRATIONS_DIR"
  IMAGE_OWNER="$IMAGE_OWNER"
  IMAGE_TAG="$DEPLOY_TAG"
  docker compose
  -p "$COMPOSE_PROJECT_NAME"
  --project-directory "$PROJECT_DIRECTORY"
  -f "$COMPOSE_FILE"
  --env-file "$ENV_FILE"
)

echo "Compose diagnostics:"
echo "  pwd: $(pwd)"
echo "  DEPLOY_PATH: $DEPLOY_PATH"
echo "  compose_file: $COMPOSE_FILE"
echo "  env_file: $ENV_FILE"
echo "  MIGRATIONS_DIR: $MIGRATIONS_DIR"
echo "  project_name: $COMPOSE_PROJECT_NAME"
echo "MIGRATIONS_DIR=$MIGRATIONS_DIR"
ls -la "$MIGRATIONS_DIR"

migration_file="$MIGRATIONS_DIR/002_authn_core.sql"
if [[ ! -f "$migration_file" ]]; then
  echo "missing required migration file: $migration_file (workflow 未上传完整迁移文件)" >&2
  echo "Diagnostics: pwd" >&2
  pwd >&2
  echo "Diagnostics: ls -la $DEPLOY_PATH" >&2
  ls -la "$DEPLOY_PATH" >&2 || true
  echo "Diagnostics: ls -la $MIGRATIONS_DIR" >&2
  ls -la "$MIGRATIONS_DIR" >&2 || true
  exit 1
fi

if ! compgen -G "$MIGRATIONS_DIR/*.sql" >/dev/null; then
  echo "no SQL migrations found under $MIGRATIONS_DIR" >&2
  exit 1
fi

echo "Preflight checks..."
"${compose_cmd[@]}" config >/dev/null

echo "Resolved compose migrate volume config:"
"${compose_cmd[@]}" config | awk '
  /^  migrate:/ {in_migrate=1}
  in_migrate && /^  [^ ]/ && $1 != "migrate:" {in_migrate=0}
  in_migrate && /^    volumes:/ {in_volumes=1; print; next}
  in_migrate && in_volumes && /^      - / {print; next}
  in_migrate && in_volumes && !/^      - / {in_volumes=0}
'

echo "Logging in to ghcr.io as ${GHCR_USERNAME}"
echo "$GHCR_TOKEN" | docker login ghcr.io -u "$GHCR_USERNAME" --password-stdin >/dev/null
trap 'docker logout ghcr.io >/dev/null 2>&1 || true' EXIT

"${compose_cmd[@]}" pull auth-api admin-api

if [[ "$USE_INTERNAL_DEPS" == "true" ]]; then
  echo "Starting internal dependencies (pg, redis)..."
  "${compose_cmd[@]}" up -d pg redis

  echo "Waiting for pg to become healthy..."
  for i in {1..30}; do
    pg_container_id="$("${compose_cmd[@]}" ps -q pg)"
    if [[ -n "$pg_container_id" ]]; then
      pg_status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$pg_container_id" 2>/dev/null || true)"
    else
      pg_status=""
    fi

    if [[ "$pg_status" == "healthy" || "$pg_status" == "running" ]]; then
      echo "pg is healthy/running (status: $pg_status)."
      break
    fi

    if [[ "$i" -eq 30 ]]; then
      echo "pg did not become healthy in time." >&2
      "${compose_cmd[@]}" ps >&2 || true
      "${compose_cmd[@]}" logs pg --tail=200 >&2 || true
      exit 1
    fi

    sleep 2
  done
fi

run_migrate() {
  "${compose_cmd[@]}" run --rm migrate
}

if ! run_migrate; then
  echo "Migration failed. Diagnostics:" >&2
  docker ps >&2 || true
  echo "Diagnostics: pwd" >&2
  pwd >&2
  echo "Diagnostics: ls -la $DEPLOY_PATH" >&2
  ls -la "$DEPLOY_PATH" >&2 || true
  echo "Diagnostics: ls -la $MIGRATIONS_DIR" >&2
  ls -la "$MIGRATIONS_DIR" >&2 || true
  if [[ "$USE_INTERNAL_DEPS" == "true" ]]; then
    "${compose_cmd[@]}" logs pg --tail=200 >&2 || true
  fi
  exit 1
fi

if [[ "$USE_INTERNAL_DEPS" == "true" ]]; then
  "${compose_cmd[@]}" up -d auth-api admin-api
else
  "${compose_cmd[@]}" up -d --no-deps auth-api admin-api
fi

healthcheck() {
  curl -fsS http://127.0.0.1:8080/healthz >/dev/null
  curl -fsS http://127.0.0.1:8081/healthz >/dev/null
}

if healthcheck; then
  echo "Health checks passed."
  {
    echo "current_tag=$DEPLOY_TAG"
    echo "prev_tag=${current_tag:-$prev_tag}"
  } > "$STATE_FILE"
  echo "Deployment state updated in $STATE_FILE"
  exit 0
fi

echo "Health checks failed, starting rollback..." >&2
if [[ -z "$rollback_tag" ]]; then
  echo "No rollback tag available." >&2
  exit 1
fi

compose_cmd=(
  env
  DEPLOY_PATH="$DEPLOY_PATH"
  MIGRATIONS_DIR="$MIGRATIONS_DIR"
  IMAGE_OWNER="$IMAGE_OWNER"
  IMAGE_TAG="$rollback_tag"
  docker compose
  -p "$COMPOSE_PROJECT_NAME"
  --project-directory "$PROJECT_DIRECTORY"
  -f "$COMPOSE_FILE"
  --env-file "$ENV_FILE"
)
"${compose_cmd[@]}" pull auth-api admin-api
if [[ "$USE_INTERNAL_DEPS" == "true" ]]; then
  "${compose_cmd[@]}" up -d auth-api admin-api
else
  "${compose_cmd[@]}" up -d --no-deps auth-api admin-api
fi

if healthcheck; then
  echo "Rollback succeeded to tag: $rollback_tag"
  exit 1
fi

echo "Rollback failed. Manual intervention required." >&2
exit 1
