#!/usr/bin/env bash
set -euo pipefail

BOOTSTRAP=$(curl -sS -X POST http://localhost:8080/api/v1/auth/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"email":"smoke@example.com","password":"passw0rd!","tenant_name":"smoke-tenant"}')

read -r CODE ACCESS TENANT < <(BOOTSTRAP_JSON="$BOOTSTRAP" python3 - <<'PY'
import json, os
b=json.loads(os.environ['BOOTSTRAP_JSON'])
print(b.get('code'), b.get('data',{}).get('access_token',''), b.get('data',{}).get('tenant_id',''))
PY
)

if [[ "$CODE" != "0" || -z "$ACCESS" || -z "$TENANT" ]]; then
  echo "bootstrap failed: $BOOTSTRAP"
  exit 1
fi

ME=$(curl -sS -X GET "http://localhost:8081/api/v1/admin/tenants/${TENANT}/me/roles" \
  -H "Authorization: Bearer ${ACCESS}")

ME_CODE=$(ME_JSON="$ME" python3 - <<'PY'
import json, os
print(json.loads(os.environ['ME_JSON']).get('code'))
PY
)

if [[ "$ME_CODE" != "0" ]]; then
  echo "admin me/roles failed: $ME"
  exit 1
fi

echo "smoke ok"
