#!/usr/bin/env bash
set -euo pipefail

docker compose -f deploy/docker-compose.yml exec -T pg \
  psql -U postgres -d auth < services/auth-api/migrations/001_init.sql

echo "migrate ok"
