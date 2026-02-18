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
- POST `/api/v1/auth/bootstrap`
- POST `/api/v1/auth/register`
- POST `/api/v1/auth/login`
- POST `/api/v1/auth/refresh`
- POST `/api/v1/auth/logout`

## admin-api

- GET `/healthz`
- GET `/api/v1/admin/tenants/:tenantId/me/roles`
- POST `/api/v1/admin/tenants/:tenantId/users/:userId/roles/:role`
