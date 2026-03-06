package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/testutil"
)

func TestEmailVerificationIntegration_RegisterVerifyOTPThenLogin(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)
	testutil.FlushRedisKeys(t, rdb, "login_fail:*")

	r := newEmailVerificationIntegrationRouter(t, db, rdb)
	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "int-otp@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}

	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop register queued job: %v", err)
	}
	if !otpPattern.MatchString(job.OTP) {
		t.Fatalf("queued OTP=%q should be a 6-digit code", job.OTP)
	}

	verifyRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
		"email": "int-otp@example.com",
		"otp":   job.OTP,
	})
	if verifyRes.Code != http.StatusOK {
		t.Fatalf("verify-email status=%d want=%d body=%s", verifyRes.Code, http.StatusOK, verifyRes.Body.String())
	}

	loginRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "int-otp@example.com",
		"password": "Passw0rd!",
	})
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login status=%d want=%d body=%s", loginRes.Code, http.StatusOK, loginRes.Body.String())
	}
}

func TestEmailVerificationIntegration_RegisterVerifyMagicLinkThenLogin(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)
	testutil.FlushRedisKeys(t, rdb, "login_fail:*")

	r := newEmailVerificationIntegrationRouter(t, db, rdb)
	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "int-magic@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}

	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop register queued job: %v", err)
	}
	parsedLink, err := url.Parse(job.MagicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	token := parsedLink.Query().Get("token")
	state := parsedLink.Query().Get("state")
	if token == "" || state == "" {
		t.Fatalf("magic link missing token/state: %s", job.MagicLink)
	}

	stateCookie := findCookieByName(registerRes, magicLinkStateCookieName)
	if stateCookie == nil {
		t.Fatalf("missing %s cookie", magicLinkStateCookieName)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/verify-magic-link?token="+url.QueryEscape(token)+"&state="+url.QueryEscape(state), nil)
	req.AddCookie(stateCookie)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("verify-magic-link status=%d want=%d body=%s", w.Code, http.StatusFound, w.Body.String())
	}

	loginRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "int-magic@example.com",
		"password": "Passw0rd!",
	})
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login status=%d want=%d body=%s", loginRes.Code, http.StatusOK, loginRes.Body.String())
	}
}

func TestEmailVerificationIntegration_ResendRateLimitReturns429(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)
	testutil.FlushRedisKeys(t, rdb, "resend:*")

	r := newEmailVerificationIntegrationRouter(t, db, rdb)
	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "int-resend-limit@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}
	if _, err := popQueuedJob(t, rdb); err != nil {
		t.Fatalf("pop register queued job: %v", err)
	}

	first := performJSONRequest(t, r, http.MethodPost, "/v1/auth/resend-verification", map[string]string{"email": "int-resend-limit@example.com"})
	if first.Code != http.StatusAccepted {
		t.Fatalf("first resend status=%d want=%d body=%s", first.Code, http.StatusAccepted, first.Body.String())
	}
	if _, err := popQueuedJob(t, rdb); err != nil {
		t.Fatalf("pop first resend queued job: %v", err)
	}

	second := performJSONRequest(t, r, http.MethodPost, "/v1/auth/resend-verification", map[string]string{"email": "int-resend-limit@example.com"})
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
	if body.Data.RetryAfter <= 0 {
		t.Fatalf("retry_after=%d should be > 0", body.Data.RetryAfter)
	}
}

func TestEmailVerificationIntegration_ExpiredTokenFailsAfterFifteenMinutes(t *testing.T) {
	t.Run("otp", func(t *testing.T) {
		db := newTestDB(t)
		rdb := newTestRedis(t)
		testutil.TruncateAuthTables(t, db)
		testutil.FlushRedisKeys(t, rdb, emailQueueName)

		r := newEmailVerificationIntegrationRouter(t, db, rdb)
		registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
			"email":    "int-expired-otp@example.com",
			"password": "Passw0rd!",
		})
		if registerRes.Code != http.StatusAccepted {
			t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
		}

		job, err := popQueuedJob(t, rdb)
		if err != nil {
			t.Fatalf("pop register queued job: %v", err)
		}
		if job.ExpiresIn != "15 minutes" {
			t.Fatalf("job expires_in=%q want=%q", job.ExpiresIn, "15 minutes")
		}

		if _, err := db.Exec(context.Background(), `
update email_verifications ev
set expires_at = now() - interval '1 second'
from users u
where ev.user_id = u.id
  and u.email = $1
  and ev.token_type = 'otp'
  and ev.verified_at is null`, "int-expired-otp@example.com"); err != nil {
			t.Fatalf("expire otp verification row: %v", err)
		}

		verifyRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
			"email": "int-expired-otp@example.com",
			"otp":   job.OTP,
		})
		assertVerifyEmailErrorReason(t, verifyRes, "expired_otp")
	})

	t.Run("magic_link", func(t *testing.T) {
		db := newTestDB(t)
		rdb := newTestRedis(t)
		testutil.TruncateAuthTables(t, db)
		testutil.FlushRedisKeys(t, rdb, emailQueueName)

		r := newEmailVerificationIntegrationRouter(t, db, rdb)
		registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
			"email":    "int-expired-magic@example.com",
			"password": "Passw0rd!",
		})
		if registerRes.Code != http.StatusAccepted {
			t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
		}

		job, err := popQueuedJob(t, rdb)
		if err != nil {
			t.Fatalf("pop register queued job: %v", err)
		}
		if job.ExpiresIn != "15 minutes" {
			t.Fatalf("job expires_in=%q want=%q", job.ExpiresIn, "15 minutes")
		}

		parsedLink, err := url.Parse(job.MagicLink)
		if err != nil {
			t.Fatalf("parse magic link: %v", err)
		}
		token := parsedLink.Query().Get("token")
		state := parsedLink.Query().Get("state")
		if token == "" || state == "" {
			t.Fatalf("magic link missing token/state: %s", job.MagicLink)
		}

		if _, err := db.Exec(context.Background(), `
update email_verifications ev
set expires_at = now() - interval '1 second'
from users u
where ev.user_id = u.id
  and u.email = $1
  and ev.token_type = 'magic_link'
  and ev.verified_at is null`, "int-expired-magic@example.com"); err != nil {
			t.Fatalf("expire magic_link verification row: %v", err)
		}

		stateCookie := findCookieByName(registerRes, magicLinkStateCookieName)
		if stateCookie == nil {
			t.Fatalf("missing %s cookie", magicLinkStateCookieName)
		}
		req := httptest.NewRequest(http.MethodGet, "/v1/auth/verify-magic-link?token="+url.QueryEscape(token)+"&state="+url.QueryEscape(state), nil)
		req.AddCookie(stateCookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusGone {
			t.Fatalf("verify-magic-link status=%d want=%d body=%s", w.Code, http.StatusGone, w.Body.String())
		}
		if !strings.Contains(strings.ToLower(w.Body.String()), "expired") {
			t.Fatalf("expected expired message in body: %s", w.Body.String())
		}
	})
}

func TestEmailVerificationIntegration_CrossDeviceMagicLinkFallsBackToOTP(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)
	testutil.FlushRedisKeys(t, rdb, "login_fail:*")

	r := newEmailVerificationIntegrationRouter(t, db, rdb)
	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "int-cross-device@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}

	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop register queued job: %v", err)
	}
	parsedLink, err := url.Parse(job.MagicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	token := parsedLink.Query().Get("token")
	state := parsedLink.Query().Get("state")
	if token == "" || state == "" {
		t.Fatalf("magic link missing token/state: %s", job.MagicLink)
	}

	crossDeviceReq := httptest.NewRequest(http.MethodGet, "/v1/auth/verify-magic-link?token="+url.QueryEscape(token)+"&state="+url.QueryEscape(state), nil)
	crossDeviceReq.AddCookie(&http.Cookie{Name: magicLinkStateCookieName, Value: "wrong-state"})
	crossDeviceRes := httptest.NewRecorder()
	r.ServeHTTP(crossDeviceRes, crossDeviceReq)
	if crossDeviceRes.Code != http.StatusOK {
		t.Fatalf("cross-device verify-magic-link status=%d want=%d body=%s", crossDeviceRes.Code, http.StatusOK, crossDeviceRes.Body.String())
	}
	if !strings.Contains(crossDeviceRes.Body.String(), "manually enter the 6-digit OTP") {
		t.Fatalf("fallback page should direct user to OTP entry: %s", crossDeviceRes.Body.String())
	}

	verifyOTPRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
		"email": "int-cross-device@example.com",
		"otp":   job.OTP,
	})
	if verifyOTPRes.Code != http.StatusOK {
		t.Fatalf("otp verification status=%d want=%d body=%s", verifyOTPRes.Code, http.StatusOK, verifyOTPRes.Body.String())
	}

	loginRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "int-cross-device@example.com",
		"password": "Passw0rd!",
	})
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login status=%d want=%d body=%s", loginRes.Code, http.StatusOK, loginRes.Body.String())
	}

	var magicVerifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `
select ev.verified_at
from email_verifications ev
join users u on u.id = ev.user_id
where u.email = $1
  and ev.token_type = 'magic_link'`, "int-cross-device@example.com").Scan(&magicVerifiedAt); err != nil {
		t.Fatalf("query magic_link verification row: %v", err)
	}
	if magicVerifiedAt != nil {
		t.Fatalf("magic link should remain unverified after fallback-to-otp flow, got verified_at=%v", magicVerifiedAt)
	}
}

func newEmailVerificationIntegrationRouter(t *testing.T, db *pgxpool.Pool, rdb *goredis.Client) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := newTestAuthHandler(t, db, rdb)
	h.PasswordMinLen = 8
	h.BcryptCost = 4
	r.POST("/v1/auth/register", ginmid.Wrap(h.Register))
	r.POST("/v1/auth/verify-email", ginmid.Wrap(h.VerifyEmail))
	r.GET("/v1/auth/verify-magic-link", ginmid.Wrap(h.VerifyMagicLink))
	r.POST("/v1/auth/resend-verification", ginmid.Wrap(h.ResendVerification))
	r.POST("/v1/auth/login", func(c *gin.Context) { c.Request.RemoteAddr = "192.0.2.1:12345"; ginmid.Wrap(h.Login)(c) })
	return r
}
