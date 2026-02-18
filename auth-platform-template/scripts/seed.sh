#!/usr/bin/env bash
set -euo pipefail

curl -sS -X POST http://localhost:8080/api/v1/auth/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"email":"seed@example.com","password":"passw0rd!","tenant_name":"seed-tenant"}'

echo
