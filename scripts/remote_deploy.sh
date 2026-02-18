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

COMPOSE_FILE="deploy/docker-compose.prod.yml"
STATE_FILE=".deploy_state"
ENV_FILE=".env"

mkdir -p "$DEPLOY_PATH"
cd "$DEPLOY_PATH"

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

export IMAGE_OWNER
export IMAGE_TAG="$DEPLOY_TAG"

compose_cmd=(docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE")
if [[ "$USE_INTERNAL_DEPS" == "true" ]]; then
  compose_cmd+=(--profile with-deps)
fi

echo "Logging in to ghcr.io as ${GHCR_USERNAME}"
echo "$GHCR_TOKEN" | docker login ghcr.io -u "$GHCR_USERNAME" --password-stdin >/dev/null
trap 'docker logout ghcr.io >/dev/null 2>&1 || true' EXIT

"${compose_cmd[@]}" pull auth-api admin-api
"${compose_cmd[@]}" run --rm migrate
"${compose_cmd[@]}" up -d auth-api admin-api

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

export IMAGE_TAG="$rollback_tag"
"${compose_cmd[@]}" pull auth-api admin-api
"${compose_cmd[@]}" up -d auth-api admin-api

if healthcheck; then
  echo "Rollback succeeded to tag: $rollback_tag"
  exit 1
fi

echo "Rollback failed. Manual intervention required." >&2
exit 1
