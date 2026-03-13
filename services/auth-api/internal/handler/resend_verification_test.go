package handler

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/modules/common-go/pkg/queue"
	"anvilkit-auth-template/services/auth-api/internal/testutil"
)

func TestResendVerificationSuccess(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)
	testutil.FlushRedisKeys(t, rdb, "resend:*")

	r := newResendVerificationRouter(t, db, rdb)
	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "resend-ok@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}
	if _, err := popQueuedJob(t, rdb); err != nil {
		t.Fatalf("pop register queued job: %v", err)
	}

	oldExpiresByID := map[string]time.Time{}
	rows, err := db.Query(context.Background(), `
select ev.id, ev.expires_at
from email_verifications ev
join users u on u.id = ev.user_id
where u.email = $1
  and ev.verified_at is null
  and ev.expires_at > now()`,
		"resend-ok@example.com",
	)
	if err != nil {
		t.Fatalf("query old verification rows: %v", err)
	}
	for rows.Next() {
		var id string
		var expiresAt time.Time
		if err := rows.Scan(&id, &expiresAt); err != nil {
			t.Fatalf("scan old verification row: %v", err)
		}
		oldExpiresByID[id] = expiresAt
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatalf("iterate old verification rows: %v", rows.Err())
	}
	if len(oldExpiresByID) != 2 {
		t.Fatalf("old active verification row count=%d want=2", len(oldExpiresByID))
	}

	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/resend-verification", map[string]string{
		"email": "resend-ok@example.com",
	})
	if res.Code != http.StatusAccepted {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusAccepted, res.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Message    string `json:"message"`
			RetryAfter int    `json:"retry_after"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != 0 {
		t.Fatalf("code=%d want=0 body=%s", body.Code, res.Body.String())
	}
	if body.Data.Message != resendVerificationMessage || body.Data.RetryAfter != 90 {
		t.Fatalf("unexpected response data: %+v", body.Data)
	}

	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop resend queued job: %v", err)
	}
	if job.To != "resend-ok@example.com" {
		t.Fatalf("job to=%q want=%q", job.To, "resend-ok@example.com")
	}
	if !otpPattern.MatchString(job.OTP) {
		t.Fatalf("job otp=%q should be a 6-digit numeric code", job.OTP)
	}
	parsedMagicLink, err := url.Parse(job.MagicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	token := parsedMagicLink.Query().Get("token")
	state := parsedMagicLink.Query().Get("state")
	if token == "" || state == "" {
		t.Fatalf("job magic_link=%q missing token/state", job.MagicLink)
	}
	if job.ExpiresIn != "15 minutes" {
		t.Fatalf("job expires_in=%q want=%q", job.ExpiresIn, "15 minutes")
	}
	if job.ResendIn != "90 seconds" {
		t.Fatalf("job resend_in=%q want=%q", job.ResendIn, "90 seconds")
	}
	if job.TextBody != "" || job.HTMLBody != "" {
		t.Fatalf("expected queue payload bodies to be empty for worker-side template rendering: text=%q html=%q", job.TextBody, job.HTMLBody)
	}

	stateCookie := findCookieByName(res, magicLinkStateCookieName)
	if stateCookie == nil {
		t.Fatalf("expected %s cookie to be set", magicLinkStateCookieName)
	}
	if stateCookie.Value != state {
		t.Fatalf("cookie state=%q want=%q", stateCookie.Value, state)
	}

	for id, oldExpiresAt := range oldExpiresByID {
		var newExpiresAt time.Time
		if err := db.QueryRow(context.Background(), `select expires_at from email_verifications where id=$1`, id).Scan(&newExpiresAt); err != nil {
			t.Fatalf("query updated expires_at by id: %v", err)
		}
		if !newExpiresAt.Before(oldExpiresAt) {
			t.Fatalf("verification row %s expires_at=%s should be earlier than old value=%s", id, newExpiresAt, oldExpiresAt)
		}
	}

	var activeCount int
	if err := db.QueryRow(context.Background(), `
select count(1)
from email_verifications ev
join users u on u.id = ev.user_id
where u.email = $1
  and ev.verified_at is null
  and ev.expires_at > now()`,
		"resend-ok@example.com",
	).Scan(&activeCount); err != nil {
		t.Fatalf("count active verification rows: %v", err)
	}
	if activeCount != 2 {
		t.Fatalf("active verification row count=%d want=2", activeCount)
	}

	var emailRecordCount int
	if err := db.QueryRow(context.Background(), `
select count(1)
from email_records er
join users u on u.id = er.user_id
where u.email = $1`,
		"resend-ok@example.com",
	).Scan(&emailRecordCount); err != nil {
		t.Fatalf("count email records: %v", err)
	}
	if emailRecordCount != 2 {
		t.Fatalf("email record count=%d want=2", emailRecordCount)
	}
}

func TestResendVerificationRateLimit(t *testing.T) {
	t.Run("cooldown blocks immediate resend", func(t *testing.T) {
		db := newTestDB(t)
		rdb := newTestRedis(t)
		testutil.TruncateAuthTables(t, db)
		testutil.FlushRedisKeys(t, rdb, emailQueueName)
		testutil.FlushRedisKeys(t, rdb, "resend:*")

		r := newResendVerificationRouter(t, db, rdb)
		registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
			"email":    "resend-cooldown@example.com",
			"password": "Passw0rd!",
		})
		if registerRes.Code != http.StatusAccepted {
			t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
		}
		if _, err := popQueuedJob(t, rdb); err != nil {
			t.Fatalf("pop register queued job: %v", err)
		}

		first := performJSONRequest(t, r, http.MethodPost, "/v1/auth/resend-verification", map[string]string{
			"email": "resend-cooldown@example.com",
		})
		if first.Code != http.StatusAccepted {
			t.Fatalf("first resend status=%d want=%d body=%s", first.Code, http.StatusAccepted, first.Body.String())
		}
		if _, err := popQueuedJob(t, rdb); err != nil {
			t.Fatalf("pop first resend queued job: %v", err)
		}

		second := performJSONRequest(t, r, http.MethodPost, "/v1/auth/resend-verification", map[string]string{
			"email": "resend-cooldown@example.com",
		})
		if second.Code != http.StatusTooManyRequests {
			t.Fatalf("second resend status=%d want=%d body=%s", second.Code, http.StatusTooManyRequests, second.Body.String())
		}
		var body struct {
			Code int `json:"code"`
			Data struct {
				Reason     string `json:"reason"`
				RetryAfter int    `json:"retry_after"`
			} `json:"data"`
		}
		decodeResponse(t, second, &body)
		if body.Code != errcode.RateLimited {
			t.Fatalf("code=%d want=%d", body.Code, errcode.RateLimited)
		}
		if body.Data.Reason != "cooldown_active" {
			t.Fatalf("reason=%q want=%q", body.Data.Reason, "cooldown_active")
		}
		if body.Data.RetryAfter <= 0 || body.Data.RetryAfter > int(resendVerificationWindow/time.Second) {
			t.Fatalf("retry_after=%d should be within (0,%d]", body.Data.RetryAfter, int(resendVerificationWindow/time.Second))
		}
	})

	t.Run("over limit returns too_many_requests", func(t *testing.T) {
		db := newTestDB(t)
		rdb := newTestRedis(t)
		testutil.TruncateAuthTables(t, db)
		testutil.FlushRedisKeys(t, rdb, emailQueueName)
		testutil.FlushRedisKeys(t, rdb, "resend:*")

		r := newResendVerificationRouter(t, db, rdb)
		registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
			"email":    "resend-over-limit@example.com",
			"password": "Passw0rd!",
		})
		if registerRes.Code != http.StatusAccepted {
			t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
		}
		if _, err := popQueuedJob(t, rdb); err != nil {
			t.Fatalf("pop register queued job: %v", err)
		}

		key := "resend:resend-over-limit@example.com"
		if err := rdb.Set(context.Background(), key, resendVerificationLimit, resendVerificationWindow).Err(); err != nil {
			t.Fatalf("preset resend key: %v", err)
		}

		res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/resend-verification", map[string]string{
			"email": "resend-over-limit@example.com",
		})
		if res.Code != http.StatusTooManyRequests {
			t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusTooManyRequests, res.Body.String())
		}
		var body struct {
			Code int `json:"code"`
			Data struct {
				Reason     string `json:"reason"`
				RetryAfter int    `json:"retry_after"`
			} `json:"data"`
		}
		decodeResponse(t, res, &body)
		if body.Code != errcode.RateLimited {
			t.Fatalf("code=%d want=%d", body.Code, errcode.RateLimited)
		}
		if body.Data.Reason != "too_many_requests" {
			t.Fatalf("reason=%q want=%q", body.Data.Reason, "too_many_requests")
		}
		if body.Data.RetryAfter <= 0 || body.Data.RetryAfter > int(resendVerificationWindow/time.Second) {
			t.Fatalf("retry_after=%d should be within (0,%d]", body.Data.RetryAfter, int(resendVerificationWindow/time.Second))
		}

		q, err := queue.New(rdb)
		if err != nil {
			t.Fatalf("new queue: %v", err)
		}
		n, err := q.QueueLength(emailQueueName)
		if err != nil {
			t.Fatalf("queue length: %v", err)
		}
		if n != 0 {
			t.Fatalf("queue length=%d want=0", n)
		}
	})
}

func TestResendVerificationVerifiedUserReturnsBadRequest(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)
	testutil.FlushRedisKeys(t, rdb, "resend:*")

	if _, err := db.Exec(context.Background(), `
insert into users(id,email,status,email_verified_at,created_at,updated_at)
values($1,$2,1,now(),now(),now())`,
		"verified-resend-user",
		"verified-resend@example.com",
	); err != nil {
		t.Fatalf("seed verified user: %v", err)
	}

	r := newResendVerificationRouter(t, db, rdb)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/resend-verification", map[string]string{
		"email": "verified-resend@example.com",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusBadRequest, res.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.BadRequest {
		t.Fatalf("code=%d want=%d", body.Code, errcode.BadRequest)
	}
	if body.Data.Reason != "resend_not_allowed" {
		t.Fatalf("reason=%q want=%q", body.Data.Reason, "resend_not_allowed")
	}

	q, err := queue.New(rdb)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	n, err := q.QueueLength(emailQueueName)
	if err != nil {
		t.Fatalf("queue length: %v", err)
	}
	if n != 0 {
		t.Fatalf("queue length=%d want=0", n)
	}
}

func TestResendVerificationUserNotFoundReturnsGenericBadRequest(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)
	testutil.FlushRedisKeys(t, rdb, "resend:*")

	r := newResendVerificationRouter(t, db, rdb)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/resend-verification", map[string]string{
		"email": "missing-resend-user@example.com",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusBadRequest, res.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.BadRequest {
		t.Fatalf("code=%d want=%d", body.Code, errcode.BadRequest)
	}
	if body.Data.Reason != "resend_not_allowed" {
		t.Fatalf("reason=%q want=%q", body.Data.Reason, "resend_not_allowed")
	}
}

func newResendVerificationRouter(t *testing.T, db *pgxpool.Pool, rdb *goredis.Client) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := newTestAuthHandler(t, db, rdb)
	r.POST("/v1/auth/register", ginmid.Wrap(h.Register))
	r.POST("/v1/auth/resend-verification", ginmid.Wrap(h.ResendVerification))
	return r
}
