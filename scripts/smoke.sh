#!/usr/bin/env bash
set -euo pipefail

BOOTSTRAP=$(curl -sS -X POST http://localhost:8080/v1/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"tenant_name":"smoke-tenant","owner_email":"smoke@example.com","owner_password":"Passw0rd!"}')

read -r CODE TENANT < <(BOOTSTRAP_JSON="$BOOTSTRAP" python3 - <<'PY'
import json, os
b=json.loads(os.environ['BOOTSTRAP_JSON'])
print(b.get('code'), b.get('data',{}).get('tenant',{}).get('id',''))
PY
)

if [[ "$CODE" != "0" || -z "$TENANT" ]]; then
  echo "bootstrap failed: $BOOTSTRAP"
  exit 1
fi

LOGIN=$(curl -sS -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"smoke@example.com","password":"Passw0rd!"}')

read -r LOGIN_CODE ACCESS < <(LOGIN_JSON="$LOGIN" python3 - <<'PY'
import json, os
b=json.loads(os.environ['LOGIN_JSON'])
print(b.get('code'), b.get('data',{}).get('access_token',''))
PY
)

if [[ "$LOGIN_CODE" != "0" || -z "$ACCESS" ]]; then
  echo "login failed: $LOGIN"
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
