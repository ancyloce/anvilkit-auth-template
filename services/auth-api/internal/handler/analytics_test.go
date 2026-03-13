package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/testutil"
)

func TestRegisterEmitsVerificationRegistrationStarted(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	tracker := &fakeAnalytics{}
	r := newAnalyticsRouter(t, db, rdb, tracker)

	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "analytics-register@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", res.Code, http.StatusAccepted, res.Body.String())
	}

	events := capturedEventsByName(tracker.events, "verification_registration_started")
	if len(events) != 1 {
		t.Fatalf("verification_registration_started count=%d want=1 events=%+v", len(events), tracker.events)
	}
	event := events[0]
	if event.Email != "analytics-register@example.com" || event.UserID == "" {
		t.Fatalf("unexpected event identity: %+v", event)
	}
	if event.Properties["flow"] != "register" {
		t.Fatalf("flow=%v want=register", event.Properties["flow"])
	}
	if ttl, ok := event.Properties["verification_ttl_seconds"].(int64); !ok || ttl != 900 {
		t.Fatalf("verification_ttl_seconds=%v want int64(900)", event.Properties["verification_ttl_seconds"])
	}
}

func TestVerifyEmailEmitsAccountActivatedWithOTPMethod(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	tracker := &fakeAnalytics{}
	r := newAnalyticsRouter(t, db, rdb, tracker)

	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "analytics-otp@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}
	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}

	verifyRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
		"email": "analytics-otp@example.com",
		"otp":   job.OTP,
	})
	if verifyRes.Code != http.StatusOK {
		t.Fatalf("verify status=%d want=%d body=%s", verifyRes.Code, http.StatusOK, verifyRes.Body.String())
	}

	events := capturedEventsByName(tracker.events, "account_activated")
	if len(events) != 1 {
		t.Fatalf("account_activated count=%d want=1 events=%+v", len(events), tracker.events)
	}
	event := events[0]
	if event.Email != "analytics-otp@example.com" || event.UserID == "" {
		t.Fatalf("unexpected event identity: %+v", event)
	}
	if event.Properties["method"] != "otp" {
		t.Fatalf("method=%v want=otp", event.Properties["method"])
	}
	if event.Timestamp.IsZero() {
		t.Fatal("timestamp should be set")
	}
}

func TestVerifyMagicLinkEmitsClickAndActivationEvents(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	tracker := &fakeAnalytics{}
	r := newAnalyticsRouter(t, db, rdb, tracker)

	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "analytics-magic@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}
	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}

	var recordID string
	if err := db.QueryRow(testContext(t), `select id from email_records where to_email=$1 order by created_at desc limit 1`, "analytics-magic@example.com").Scan(&recordID); err != nil {
		t.Fatalf("query email record: %v", err)
	}
	if _, err := db.Exec(testContext(t), `update email_records set external_id='esp-analytics' where id=$1`, recordID); err != nil {
		t.Fatalf("set external id: %v", err)
	}
	if _, err := db.Exec(testContext(t), `insert into email_status_history(id,email_record_id,status,message,created_at) values('status-sent', $1, 'sent', 'sent', now()-interval '2 minutes')`, recordID); err != nil {
		t.Fatalf("insert sent history: %v", err)
	}

	parsedLink, err := url.Parse(job.MagicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	stateCookie := findCookieByName(registerRes, magicLinkStateCookieName)
	if stateCookie == nil {
		t.Fatalf("missing %s cookie", magicLinkStateCookieName)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/verify-magic-link?token="+url.QueryEscape(parsedLink.Query().Get("token"))+"&state="+url.QueryEscape(parsedLink.Query().Get("state")), nil)
	req.AddCookie(stateCookie)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("verify magic link status=%d want=%d body=%s", w.Code, http.StatusFound, w.Body.String())
	}

	clickedEvents := capturedEventsByName(tracker.events, "verification_link_clicked")
	if len(clickedEvents) != 1 {
		t.Fatalf("verification_link_clicked count=%d want=1 events=%+v", len(clickedEvents), tracker.events)
	}
	clicked := clickedEvents[0]
	if clicked.Email != "analytics-magic@example.com" || clicked.UserID == "" {
		t.Fatalf("unexpected clicked event identity: %+v", clicked)
	}
	latency, ok := clicked.Properties["latency_from_sent"].(int64)
	if !ok {
		t.Fatalf("latency_from_sent type=%T want int64", clicked.Properties["latency_from_sent"])
	}
	if latency <= 0 {
		t.Fatalf("latency_from_sent=%d want > 0", latency)
	}
	activatedEvents := capturedEventsByName(tracker.events, "account_activated")
	if len(activatedEvents) != 1 {
		t.Fatalf("account_activated count=%d want=1 events=%+v", len(activatedEvents), tracker.events)
	}
	activated := activatedEvents[0]
	if activated.Properties["method"] != "magic_link" {
		t.Fatalf("method=%v want magic_link", activated.Properties["method"])
	}
}

func TestVerifyMagicLinkExpiredDoesNotEmitClickedEvent(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	tracker := &fakeAnalytics{}
	r := newAnalyticsRouter(t, db, rdb, tracker)

	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "analytics-expired-click@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}
	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}
	parsedLink, err := url.Parse(job.MagicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	if _, err := db.Exec(testContext(t), `
update email_verifications ev
set expires_at = now() - interval '1 second'
from users u
where ev.user_id = u.id
  and u.email = $1
  and ev.token_type = 'magic_link'
  and ev.verified_at is null`, "analytics-expired-click@example.com"); err != nil {
		t.Fatalf("expire magic_link verification row: %v", err)
	}

	stateCookie := findCookieByName(registerRes, magicLinkStateCookieName)
	if stateCookie == nil {
		t.Fatalf("missing %s cookie", magicLinkStateCookieName)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/verify-magic-link?token="+url.QueryEscape(parsedLink.Query().Get("token"))+"&state="+url.QueryEscape(parsedLink.Query().Get("state")), nil)
	req.AddCookie(stateCookie)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("verify magic link status=%d want=%d body=%s", w.Code, http.StatusGone, w.Body.String())
	}
	if len(capturedEventsByName(tracker.events, "verification_link_clicked")) != 0 {
		t.Fatalf("verification_link_clicked should not be emitted: %+v", tracker.events)
	}
}

func TestVerifyEmailEmitsOTPFailureReason(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	tracker := &fakeAnalytics{}
	r := newAnalyticsRouter(t, db, rdb, tracker)

	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "analytics-otp-failure@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}
	if _, err := popQueuedJob(t, rdb); err != nil {
		t.Fatalf("pop queued job: %v", err)
	}

	verifyRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
		"email": "analytics-otp-failure@example.com",
		"otp":   "000000",
	})
	if verifyRes.Code != http.StatusBadRequest {
		t.Fatalf("verify status=%d want=%d body=%s", verifyRes.Code, http.StatusBadRequest, verifyRes.Body.String())
	}

	events := capturedEventsByName(tracker.events, "verification_otp_failed")
	if len(events) != 1 {
		t.Fatalf("verification_otp_failed count=%d want=1 events=%+v", len(events), tracker.events)
	}
	if events[0].Properties["reason"] != "invalid_otp" {
		t.Fatalf("reason=%v want=invalid_otp", events[0].Properties["reason"])
	}
}

func TestVerifyEmailAfterMagicLinkDoesNotEmitSecondActivation(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	tracker := &fakeAnalytics{}
	r := newAnalyticsRouter(t, db, rdb, tracker)

	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "analytics-dupe-otp@example.com",
		"password": "Passw0rd!",
	})
	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}
	parsedLink, err := url.Parse(job.MagicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	stateCookie := findCookieByName(registerRes, magicLinkStateCookieName)
	if stateCookie == nil {
		t.Fatalf("missing %s cookie", magicLinkStateCookieName)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/verify-magic-link?token="+url.QueryEscape(parsedLink.Query().Get("token"))+"&state="+url.QueryEscape(parsedLink.Query().Get("state")), nil)
	req.AddCookie(stateCookie)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("verify magic link status=%d want=%d body=%s", w.Code, http.StatusFound, w.Body.String())
	}

	verifyRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
		"email": "analytics-dupe-otp@example.com",
		"otp":   job.OTP,
	})
	if verifyRes.Code != http.StatusOK {
		t.Fatalf("verify otp status=%d want=%d body=%s", verifyRes.Code, http.StatusOK, verifyRes.Body.String())
	}

	activationCount := 0
	for _, event := range tracker.events {
		if event.Name == "account_activated" {
			activationCount++
		}
	}
	if activationCount != 1 {
		t.Fatalf("account_activated count=%d want=1 events=%+v", activationCount, tracker.events)
	}
}

func TestVerifyMagicLinkAfterOTPDoesNotEmitSecondActivation(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	tracker := &fakeAnalytics{}
	r := newAnalyticsRouter(t, db, rdb, tracker)

	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "analytics-dupe-magic@example.com",
		"password": "Passw0rd!",
	})
	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}
	verifyRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
		"email": "analytics-dupe-magic@example.com",
		"otp":   job.OTP,
	})
	if verifyRes.Code != http.StatusOK {
		t.Fatalf("verify otp status=%d want=%d body=%s", verifyRes.Code, http.StatusOK, verifyRes.Body.String())
	}

	parsedLink, err := url.Parse(job.MagicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	stateCookie := findCookieByName(registerRes, magicLinkStateCookieName)
	if stateCookie == nil {
		t.Fatalf("missing %s cookie", magicLinkStateCookieName)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/verify-magic-link?token="+url.QueryEscape(parsedLink.Query().Get("token"))+"&state="+url.QueryEscape(parsedLink.Query().Get("state")), nil)
	req.AddCookie(stateCookie)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("verify magic link status=%d want=%d body=%s", w.Code, http.StatusFound, w.Body.String())
	}

	activationCount := 0
	for _, event := range tracker.events {
		if event.Name == "account_activated" {
			activationCount++
		}
	}
	if activationCount != 1 {
		t.Fatalf("account_activated count=%d want=1 events=%+v", activationCount, tracker.events)
	}
}

func newAnalyticsRouter(t *testing.T, db *pgxpool.Pool, rdb *goredis.Client, tracker *fakeAnalytics) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := newTestAuthHandler(t, db, rdb)
	h.Analytics = tracker
	r.POST("/v1/auth/register", ginmid.Wrap(h.Register))
	r.POST("/v1/auth/verify-email", ginmid.Wrap(h.VerifyEmail))
	r.GET("/v1/auth/verify-magic-link", ginmid.Wrap(h.VerifyMagicLink))
	return r
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func capturedEventsByName(events []capturedEvent, name string) []capturedEvent {
	filtered := make([]capturedEvent, 0, len(events))
	for _, event := range events {
		if event.Name == name {
			filtered = append(filtered, event)
		}
	}
	return filtered
}
