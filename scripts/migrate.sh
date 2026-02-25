#!/usr/bin/env bash
set -euo pipefail

for dir in services/auth-api/migrations services/admin-api/migrations; do
  for file in $(find "$dir" -maxdepth 1 -type f -name '*.sql' | sort); do
    docker compose -f deploy/docker-compose.yml exec -T pg \
      psql -U postgres -d auth -v ON_ERROR_STOP=1 -f - < "$file"
    echo "applied migration: $file"
  done
done

echo "migrate ok"
