# Improvement Roadmap: AnvilKit Auth Template

**Document Version:** 1.0
**Date:** March 1, 2026
**Status:** Draft

---

## Overview

This document provides a prioritized roadmap for improving the AnvilKit Auth Template based on the comprehensive technical review. Items are categorized by priority and estimated effort.

---

## Priority Levels

- **🔴 Critical:** Must fix before production deployment (blockers)
- **🟠 High:** Should fix within 1-2 weeks of launch
- **🟡 Medium:** Nice to have, improves maintainability
- **🟢 Low:** Future enhancements, not urgent

---

## 🔴 Critical Priority (Must Fix Before Production)

### 1. Implement Graceful Shutdown

**Issue:** Services don't handle SIGTERM/SIGINT signals, causing abrupt termination during deployments.

**Impact:**
- In-flight requests aborted
- Database connections not closed properly
- Zero-downtime deployments impossible

**Affected Files:**
- `services/auth-api/cmd/auth-api/main.go`
- `services/admin-api/cmd/admin-api/main.go`

**Implementation:**
```go
// Add signal handling and graceful shutdown
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit

ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
srv.Shutdown(ctx)
```

**Estimated Effort:** 4 hours
**Dependencies:** None

---

### 2. Add Request Timeouts

**Issue:** No timeouts configured on HTTP server or database connections.

**Impact:**
- Vulnerable to slow client attacks (slowloris)
- Resource exhaustion
- Hanging connections

**Affected Files:**
- `services/auth-api/cmd/auth-api/main.go`
- `services/admin-api/cmd/admin-api/main.go`
- `modules/common-go/pkg/dbx/postgres.go`

**Implementation:**
```go
// HTTP Server timeouts
srv := &http.Server{
    ReadTimeout:  15 * time.Second,
    WriteTimeout: 15 * time.Second,
    IdleTimeout:  60 * time.Second,
}

// Database connection timeouts
config.MaxConnLifetime = 1 * time.Hour
config.MaxConnIdleTime = 30 * time.Minute
```

**Estimated Effort:** 4 hours
**Dependencies:** None

---

### 3. Configure Connection Pooling Limits

**Issue:** Database connection pool has no max connections limit.

**Impact:**
- Database connection exhaustion
- Cascading failures under load

**Affected Files:**
- `modules/common-go/pkg/dbx/postgres.go`

**Implementation:**
```go
config.MaxConns = 25
config.MinConns = 5
config.MaxConnLifetime = 1 * time.Hour
config.HealthCheckPeriod = 1 * time.Minute
```

**Estimated Effort:** 2 hours
**Dependencies:** None

---

### 4. Implement Structured Logging

**Issue:** Uses standard `log` package with unstructured output.

**Impact:**
- Difficult to parse logs in production
- No log aggregation support
- Hard to debug issues

**Affected Files:**
- All service files
- Create `modules/common-go/pkg/logger/`

**Implementation:**
- Use `log/slog` (Go 1.21+) or `zerolog`
- JSON format for production
- Add request ID to all logs
- Log levels: DEBUG, INFO, WARN, ERROR

**Estimated Effort:** 8 hours
**Dependencies:** None

---

### 5. Add Comprehensive Health Checks

**Issue:** Basic health check doesn't verify dependencies.

**Impact:**
- Orchestrators can't determine service readiness
- Traffic routed to unhealthy instances

**Affected Files:**
- Create `services/auth-api/internal/handler/health.go`
- Create `services/admin-api/internal/handler/health.go`

**Implementation:**
```go
// GET /health/live - Liveness probe
// GET /health/ready - Readiness probe (checks DB, Redis)
```

**Estimated Effort:** 4 hours
**Dependencies:** None

---

**Total Critical Priority Effort:** ~22 hours (3 days)

---

## 🟠 High Priority (Fix Within 1-2 Weeks)

### 6. Add Rate Limiting to Admin API

**Issue:** Admin API has no rate limiting, vulnerable to brute-force.

**Affected Files:**
- `services/admin-api/cmd/admin-api/main.go`

**Implementation:**
- Apply rate limiting middleware to all admin endpoints
- Use same Redis-backed rate limiter as auth-api

**Estimated Effort:** 3 hours
**Dependencies:** None

---

### 7. Add Rate Limiting to Registration Endpoint

**Issue:** Registration endpoint vulnerable to spam/DoS.

**Affected Files:**
- `services/auth-api/internal/handler/auth.go`

**Implementation:**
- Add rate limiting to `POST /api/auth/register`
- Limit: 5 registrations per IP per hour

**Estimated Effort:** 2 hours
**Dependencies:** None

---

### 8. Implement Prometheus Metrics

**Issue:** No metrics for monitoring service health and performance.

**Affected Files:**
- Create `modules/common-go/pkg/httpx/ginmid/metrics.go`
- Both service main.go files

**Metrics to Track:**
- Request duration histogram
- Request count by endpoint and status
- Active connections
- Database connection pool stats
- Redis connection stats

**Implementation:**
```go
import "github.com/prometheus/client_golang/prometheus"

// Add /metrics endpoint
router.GET("/metrics", gin.WrapH(promhttp.Handler()))
```

**Estimated Effort:** 8 hours
**Dependencies:** None

---

### 9. Add Distributed Tracing

**Issue:** No request tracing across services.

**Affected Files:**
- Create `modules/common-go/pkg/tracing/`
- Both service main.go files

**Implementation:**
- Use OpenTelemetry
- Trace HTTP requests
- Trace database queries
- Export to Jaeger or Tempo

**Estimated Effort:** 12 hours
**Dependencies:** Metrics implementation

---

### 10. Expand Handler Test Coverage

**Issue:** No tests for HTTP handlers.

**Affected Files:**
- Create `services/auth-api/internal/handler/*_test.go`
- Create `services/admin-api/internal/handler/*_test.go`

**Test Coverage Goals:**
- All handler functions
- Request validation
- Error responses
- Success responses

**Estimated Effort:** 16 hours
**Dependencies:** None

---

### 11. Add Input Validation Improvements

**Issue:** Missing email format validation, phone validation, request size limits.

**Affected Files:**
- `services/auth-api/internal/handler/dto.go`
- Create `modules/common-go/pkg/httpx/ginmid/validation.go`

**Implementation:**
- Add email format validation: `binding:"required,email"`
- Add phone number validation (E.164 format)
- Add request size limit middleware (1MB)
- Add custom validators for complex rules

**Estimated Effort:** 6 hours
**Dependencies:** None

---

### 12. Add Security Headers Middleware

**Issue:** Missing security headers (CSP, HSTS, X-Frame-Options).

**Affected Files:**
- Create `modules/common-go/pkg/httpx/ginmid/security_headers.go`

**Headers to Add:**
- `Content-Security-Policy`
- `Strict-Transport-Security`
- `X-Frame-Options: DENY`
- `X-Content-Type-Options: nosniff`
- `X-XSS-Protection: 1; mode=block`

**Estimated Effort:** 3 hours
**Dependencies:** None

---

### 13. Fix Unsafe Type Assertion in Admin API

**Issue:** `c.GetInt64("uid")` can panic if key doesn't exist.

**Affected Files:**
- `services/admin-api/internal/handler/rbac.go`

**Implementation:**
```go
uid, exists := c.Get("uid")
if !exists {
    c.JSON(401, gin.H{"error": "unauthorized"})
    return
}
uidInt64, ok := uid.(int64)
if !ok {
    c.JSON(500, gin.H{"error": "invalid user id type"})
    return
}
```

**Estimated Effort:** 1 hour
**Dependencies:** None

---

### 14. Fix JWT Claims Nil Check

**Issue:** `ValidateTokenType` doesn't check if claims is nil before accessing fields.

**Affected Files:**
- `modules/common-go/pkg/auth/jwt.go`

**Implementation:**
```go
if claims == nil || claims.Type != expectedType {
    return nil, ErrInvalidTokenType
}
```

**Estimated Effort:** 1 hour
**Dependencies:** None

---

**Total High Priority Effort:** ~52 hours (6-7 days)

---

## 🟡 Medium Priority (Nice to Have)

### 15. Extract Configuration to Common Module

**Issue:** Configuration loading inconsistent between services.

**Affected Files:**
- Create `modules/common-go/pkg/config/`
- Refactor both services to use shared config

**Benefits:**
- Consistent validation
- Reduced duplication
- Easier to add new config options

**Estimated Effort:** 8 hours
**Dependencies:** None

---

### 16. Create Transaction Helper Function

**Issue:** Transaction rollback boilerplate repeated in multiple files.

**Affected Files:**
- Create `modules/common-go/pkg/dbx/transaction.go`
- Refactor all store files

**Implementation:**
```go
func WithTransaction(ctx context.Context, db *pgxpool.Pool, fn func(pgx.Tx) error) error {
    tx, err := db.Begin(ctx)
    if err != nil {
        return err
    }
    defer func() {
        if err != nil {
            tx.Rollback(ctx)
        }
    }()
    err = fn(tx)
    if err != nil {
        return err
    }
    return tx.Commit(ctx)
}
```

**Estimated Effort:** 6 hours
**Dependencies:** None

---

### 17. Extract Token Hashing to Utility Function

**Issue:** Token hashing code duplicated in refresh_session.go.

**Affected Files:**
- Create `modules/common-go/pkg/auth/token_hash.go`
- Refactor `services/auth-api/internal/store/refresh_session.go`

**Implementation:**
```go
func HashToken(token string) string {
    hash := sha256.Sum256([]byte(token))
    return hex.EncodeToString(hash[:])
}
```

**Estimated Effort:** 2 hours
**Dependencies:** None

---

### 18. Add Database Migration Rollback Support

**Issue:** Migrations are forward-only, no rollback capability.

**Affected Files:**
- All migration files in `services/*/migrations/`

**Implementation:**
- Create `.down.sql` files for each migration
- Use migration tool with rollback support (golang-migrate)

**Estimated Effort:** 8 hours
**Dependencies:** None

---

### 19. Add Missing Database Indexes

**Issue:** No index on `users.email` and `refresh_sessions.user_id`.

**Affected Files:**
- Create new migration file

**Implementation:**
```sql
CREATE UNIQUE INDEX idx_users_email ON users(email) WHERE email IS NOT NULL;
CREATE INDEX idx_refresh_sessions_user_id ON refresh_sessions(user_id);
CREATE INDEX idx_refresh_sessions_expires_at ON refresh_sessions(expires_at);
```

**Estimated Effort:** 2 hours
**Dependencies:** None

---

### 20. Implement Token Cleanup Job

**Issue:** Expired refresh tokens accumulate in database.

**Affected Files:**
- Create `services/auth-api/cmd/cleanup/main.go`
- Add cron job to docker-compose

**Implementation:**
```sql
DELETE FROM refresh_sessions
WHERE expires_at < NOW() - INTERVAL '30 days';
```

**Estimated Effort:** 6 hours
**Dependencies:** None

---

### 21. Expand RBAC Policies

**Issue:** Only 2 policies defined, `member` role has no explicit policy.

**Affected Files:**
- `services/admin-api/internal/rbac/policy.csv`

**Implementation:**
```csv
p, tenant_admin, tenant:*, *, *
p, org_admin, tenant:*, users, read
p, org_admin, tenant:*, roles, read
p, member, tenant:*, users, read_self
```

**Estimated Effort:** 4 hours
**Dependencies:** None

---

### 22. Add Role Audit Trail

**Issue:** No audit log for role changes.

**Affected Files:**
- Create migration for `role_audit_log` table
- Update `services/admin-api/internal/store/rbac.go`

**Implementation:**
```sql
CREATE TABLE role_audit_log (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    role VARCHAR(50) NOT NULL,
    action VARCHAR(20) NOT NULL,
    performed_by BIGINT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
```

**Estimated Effort:** 8 hours
**Dependencies:** None

---

### 23. Add E2E Tests

**Issue:** No end-to-end tests for full user journeys.

**Affected Files:**
- Create `tests/e2e/` directory
- Add test files for each user journey

**Test Scenarios:**
- User registration → login → refresh → revoke
- Multi-tenant isolation
- RBAC permission checks
- Rate limiting effectiveness

**Estimated Effort:** 16 hours
**Dependencies:** None

---

**Total Medium Priority Effort:** ~60 hours (7-8 days)

---

## 🟢 Low Priority (Future Enhancements)

### 24. Add Password Complexity Requirements

**Estimated Effort:** 4 hours

### 25. Implement Password History

**Estimated Effort:** 8 hours

### 26. Add Email Verification Flow

**Estimated Effort:** 16 hours

### 27. Implement 2FA/MFA

**Estimated Effort:** 24 hours

### 28. Add API Versioning

**Estimated Effort:** 8 hours

### 29. Implement Per-User Rate Limiting

**Estimated Effort:** 12 hours

### 30. Add Webhook Support for Events

**Estimated Effort:** 20 hours

### 31. Improve Refresh Token Hashing Algorithm

**Issue:** SHA256 is fast but not designed for secrets.

**Recommendation:** Use bcrypt, PBKDF2, or Argon2.

**Estimated Effort:** 4 hours

### 32. Add CORS Wildcard Validation

**Issue:** No validation to reject `*` when credentials enabled.

**Estimated Effort:** 2 hours

### 33. Document Tenant Isolation Logic

**Issue:** Tokens without `tid` can access any tenant (undocumented).

**Estimated Effort:** 1 hour

---

**Total Low Priority Effort:** ~99 hours (12-13 days)

---

## Summary

### Total Estimated Effort by Priority

| Priority | Items | Estimated Hours | Estimated Days |
|----------|-------|-----------------|----------------|
| 🔴 Critical | 5 | 22 | 3 |
| 🟠 High | 9 | 52 | 6-7 |
| 🟡 Medium | 9 | 60 | 7-8 |
| 🟢 Low | 10 | 99 | 12-13 |
| **Total** | **33** | **233** | **29-31** |

### Recommended Implementation Order

**Week 1: Critical Items (Production Blockers)**
- Days 1-3: Implement all 5 critical items

**Week 2-3: High Priority Items**
- Days 4-10: Implement high priority items

**Week 4-5: Medium Priority Items**
- Days 11-18: Implement medium priority items

**Month 2-3: Low Priority Items**
- Implement as time permits

---

## Dependencies Graph

```
Critical Items (no dependencies)
    ↓
High Priority Items
    ↓ (Metrics → Tracing)
    ↓
Medium Priority Items
    ↓
Low Priority Items
```

Most items have no dependencies and can be implemented in parallel by multiple developers.

---

**Document Status:** Ready for Review
**Next Update:** After critical items are implemented
