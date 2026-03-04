# API Summary

All APIs return envelope:

```json
{
  "request_id": "...",
  "code": 0,
  "message": "ok",
  "data": {}
}
```

Error envelope uses non-zero code and stable message.

## auth-api

- GET `/healthz`
- POST `/v1/bootstrap`
- POST `/api/v1/auth/register`
- Note: in cross-origin browser SPA flows, call `register` with credentials (`fetch(..., { credentials: "include" })`) and set `CORS_ALLOW_CREDENTIALS=true`; otherwise the `ak_magic_link_state` cookie is not persisted and magic-link same-device auto-verify cannot trigger.
- POST `/api/v1/auth/login`
- POST `/api/v1/auth/refresh`
- POST `/api/v1/auth/logout`

## admin-api

- GET `/healthz`
- GET `/api/v1/admin/tenants/:tenantId/me/roles`
- POST `/api/v1/admin/tenants/:tenantId/users/:userId/roles/:role`
