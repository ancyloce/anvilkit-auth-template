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

	if len(tracker.events) != 1 {
		t.Fatalf("event count=%d want=1", len(tracker.events))
	}
	event := tracker.events[0]
	if event.Name != "account_activated" {
		t.Fatalf("event name=%q want=account_activated", event.Name)
	}
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

	if len(tracker.events) != 2 {
		t.Fatalf("event count=%d want=2", len(tracker.events))
	}
	clicked := tracker.events[0]
	if clicked.Name != "verification_link_clicked" {
		t.Fatalf("first event=%q want verification_link_clicked", clicked.Name)
	}
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
	activated := tracker.events[1]
	if activated.Name != "account_activated" {
		t.Fatalf("second event=%q want account_activated", activated.Name)
	}
	if activated.Properties["method"] != "magic_link" {
		t.Fatalf("method=%v want magic_link", activated.Properties["method"])
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
