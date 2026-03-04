package handler

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
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

var otpPattern = regexp.MustCompile(`^\d{6}$`)

type queuedEmailJob struct {
	RecordID  string `json:"record_id"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	HTMLBody  string `json:"html_body"`
	TextBody  string `json:"text_body"`
	OTP       string `json:"otp"`
	MagicLink string `json:"magic_link"`
}

func TestRegisterSuccess(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	r := newRegisterRouter(t, db, rdb, 8)
	start := time.Now()
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "user1@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusAccepted, res.Body.String())
	}

	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			User struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"user"`
		} `json:"data"`
	}
	decodeResponse(t, res, &body)
	if body.Code != 0 || body.Message != verificationAcceptedMessage {
		t.Fatalf("unexpected envelope: %+v", body)
	}
	if body.Data.User.ID == "" {
		t.Fatal("user.id should not be empty")
	}
	if body.Data.User.Email != "user1@example.com" {
		t.Fatalf("user.email = %q, want %q", body.Data.User.Email, "user1@example.com")
	}

	var (
		userID    string
		status    int16
		credCount int
	)
	if err := db.QueryRow(context.Background(), "select id,status from users where email=$1", "user1@example.com").Scan(&userID, &status); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if status != 0 {
		t.Fatalf("user status=%d want=0", status)
	}
	if err := db.QueryRow(context.Background(), "select count(1) from user_password_credentials").Scan(&credCount); err != nil {
		t.Fatalf("query credential count: %v", err)
	}
	if credCount != 1 {
		t.Fatalf("credCount=%d want=1", credCount)
	}

	rows, err := db.Query(context.Background(), `select token_type,token_hash,expires_at from email_verifications where user_id=$1`, userID)
	if err != nil {
		t.Fatalf("query email_verifications: %v", err)
	}
	defer rows.Close()
	tokenTypes := map[string]bool{}
	verificationCount := 0
	for rows.Next() {
		var tokenType string
		var tokenHash string
		var expiresAt time.Time
		if err := rows.Scan(&tokenType, &tokenHash, &expiresAt); err != nil {
			t.Fatalf("scan email_verifications: %v", err)
		}
		verificationCount++
		tokenTypes[tokenType] = true
		if len(tokenHash) != 64 {
			t.Fatalf("token_hash length=%d want=64", len(tokenHash))
		}
		minExpiry := start.Add(14 * time.Minute)
		maxExpiry := time.Now().Add(16 * time.Minute)
		if expiresAt.Before(minExpiry) || expiresAt.After(maxExpiry) {
			t.Fatalf("expires_at=%s outside expected range [%s,%s]", expiresAt, minExpiry, maxExpiry)
		}
	}
	if rows.Err() != nil {
		t.Fatalf("iterate email_verifications: %v", rows.Err())
	}
	if verificationCount != 2 || !tokenTypes["otp"] || !tokenTypes["magic_link"] {
		t.Fatalf("verification rows=%d tokenTypes=%v want rows=2 with otp+magic_link", verificationCount, tokenTypes)
	}

	var (
		emailRecordID string
		toEmail       string
		template      string
		subject       string
		recordStatus  string
	)
	if err := db.QueryRow(context.Background(), `
select id,to_email,template,subject,status
from email_records
where user_id=$1`, userID).Scan(&emailRecordID, &toEmail, &template, &subject, &recordStatus); err != nil {
		t.Fatalf("query email_records: %v", err)
	}
	if toEmail != "user1@example.com" || template != "verification_email" || subject != verificationEmailSubject || recordStatus != "queued" {
		t.Fatalf("unexpected email record: to=%q template=%q subject=%q status=%q", toEmail, template, subject, recordStatus)
	}

	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}
	if job.RecordID != emailRecordID {
		t.Fatalf("job record_id=%q want=%q", job.RecordID, emailRecordID)
	}
	if job.To != "user1@example.com" {
		t.Fatalf("job to=%q want user1@example.com", job.To)
	}
	if !otpPattern.MatchString(job.OTP) {
		t.Fatalf("job otp=%q should be a 6-digit numeric code", job.OTP)
	}
	parsedMagicLink, err := url.Parse(job.MagicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	if parsedMagicLink.Query().Get("token") == "" || parsedMagicLink.Query().Get("state") == "" {
		t.Fatalf("job magic_link=%q missing token/state", job.MagicLink)
	}
	if !containsAll(job.TextBody, job.OTP, job.MagicLink) {
		t.Fatalf("text_body missing OTP or magic link: %q", job.TextBody)
	}
	if !containsAll(html.UnescapeString(job.HTMLBody), job.OTP, job.MagicLink) {
		t.Fatalf("html_body missing OTP or magic link: %q", job.HTMLBody)
	}

	stateCookie := findCookieByName(res, magicLinkStateCookieName)
	if stateCookie == nil || strings.TrimSpace(stateCookie.Value) == "" {
		t.Fatalf("expected %s cookie to be set", magicLinkStateCookieName)
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	r := newRegisterRouter(t, db, rdb, 8)
	first := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "dup@example.com",
		"password": "Passw0rd!",
	})
	if first.Code != http.StatusAccepted {
		t.Fatalf("first register status = %d, want %d; body = %s", first.Code, http.StatusAccepted, first.Body.String())
	}
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "dup@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusConflict, res.Body.String())
	}
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.Conflict || body.Message != "conflict" {
		t.Fatalf("unexpected conflict response: %+v", body)
	}

	q, err := queue.New(rdb)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	n, err := q.QueueLength(emailQueueName)
	if err != nil {
		t.Fatalf("queue length: %v", err)
	}
	if n != 1 {
		t.Fatalf("queue length=%d want=1", n)
	}
}

func TestRegisterWeakPassword(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	r := newRegisterRouter(t, db, rdb, 10)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "weak@example.com",
		"password": "short",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.BadRequest || body.Message != "bad_request" {
		t.Fatalf("unexpected bad request response: %+v", body)
	}

	var usersCount int
	if err := db.QueryRow(context.Background(), "select count(1) from users where email=$1", "weak@example.com").Scan(&usersCount); err != nil {
		t.Fatalf("query users count: %v", err)
	}
	if usersCount != 0 {
		t.Fatalf("usersCount = %d, want 0", usersCount)
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

func TestRegisterVerifyEmailThenLogin(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)
	testutil.FlushRedisKeys(t, rdb, "login_fail:*")

	r := newRegisterRouter(t, db, rdb, 8)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "activate@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", res.Code, http.StatusAccepted, res.Body.String())
	}

	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}

	verifyRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
		"email": "activate@example.com",
		"otp":   job.OTP,
	})
	if verifyRes.Code != http.StatusOK {
		t.Fatalf("verify status=%d want=%d body=%s", verifyRes.Code, http.StatusOK, verifyRes.Body.String())
	}
	var verifyBody struct {
		Code int `json:"code"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	decodeResponse(t, verifyRes, &verifyBody)
	if verifyBody.Code != 0 || verifyBody.Data.Message != "Email verified successfully" {
		t.Fatalf("unexpected verify response: %+v", verifyBody)
	}

	var status int16
	var emailVerifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `select status,email_verified_at from users where email=$1`, "activate@example.com").Scan(&status, &emailVerifiedAt); err != nil {
		t.Fatalf("query user after verify: %v", err)
	}
	if status != 1 {
		t.Fatalf("status=%d want=1", status)
	}
	if emailVerifiedAt == nil {
		t.Fatal("email_verified_at should be set after verification")
	}

	loginRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "activate@example.com",
		"password": "Passw0rd!",
	})
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login status=%d want=%d body=%s", loginRes.Code, http.StatusOK, loginRes.Body.String())
	}
}

func TestVerifyEmailWrongOTPReturnsInvalidOTP(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	r := newRegisterRouter(t, db, rdb, 8)
	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "wrong-otp@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}
	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}

	wrongOTP := "000000"
	if wrongOTP == job.OTP {
		wrongOTP = "999999"
	}
	verifyRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
		"email": "wrong-otp@example.com",
		"otp":   wrongOTP,
	})
	assertVerifyEmailErrorReason(t, verifyRes, "invalid_otp")
}

func TestVerifyEmailExpiredOTPReturnsExpiredOTP(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	r := newRegisterRouter(t, db, rdb, 8)
	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "expired-otp@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}
	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}

	if _, err := db.Exec(context.Background(), `
update email_verifications ev
set expires_at = now() - interval '1 minute'
from users u
where ev.user_id = u.id
  and u.email = $1
  and ev.token_type = 'otp'
  and ev.verified_at is null`, "expired-otp@example.com"); err != nil {
		t.Fatalf("expire otp row: %v", err)
	}

	verifyRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
		"email": "expired-otp@example.com",
		"otp":   job.OTP,
	})
	assertVerifyEmailErrorReason(t, verifyRes, "expired_otp")
}

func TestVerifyEmailTooManyAttemptsReturnsTooManyAttempts(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	r := newRegisterRouter(t, db, rdb, 8)
	registerRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "otp-lock@example.com",
		"password": "Passw0rd!",
	})
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", registerRes.Code, http.StatusAccepted, registerRes.Body.String())
	}
	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}

	wrongOTP := "000000"
	if wrongOTP == job.OTP {
		wrongOTP = "999999"
	}
	for i := 1; i <= 5; i++ {
		verifyRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
			"email": "otp-lock@example.com",
			"otp":   wrongOTP,
		})
		assertVerifyEmailErrorReason(t, verifyRes, "invalid_otp")
	}

	sixthRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
		"email": "otp-lock@example.com",
		"otp":   wrongOTP,
	})
	assertVerifyEmailErrorReason(t, sixthRes, "too_many_attempts")

	correctAfterLockRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]string{
		"email": "otp-lock@example.com",
		"otp":   job.OTP,
	})
	assertVerifyEmailErrorReason(t, correctAfterLockRes, "too_many_attempts")
}

func TestRegisterQueueUnavailableCleansUpPendingUser(t *testing.T) {
	db := newTestDB(t)
	testutil.TruncateAuthTables(t, db)

	r := newRegisterRouter(t, db, nil, 8)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "queue-down@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusInternalServerError, res.Body.String())
	}

	var body struct {
		Code int `json:"code"`
	}
	decodeResponse(t, res, &body)
	if body.Code != errcode.InternalError {
		t.Fatalf("code=%d want=%d", body.Code, errcode.InternalError)
	}

	var usersCount int
	if err := db.QueryRow(context.Background(), `select count(1) from users where email=$1`, "queue-down@example.com").Scan(&usersCount); err != nil {
		t.Fatalf("query users: %v", err)
	}
	if usersCount != 0 {
		t.Fatalf("users count=%d want=0", usersCount)
	}

	var verificationsCount int
	if err := db.QueryRow(context.Background(), `
select count(1)
from email_verifications ev
join users u on u.id=ev.user_id
where u.email=$1`, "queue-down@example.com").Scan(&verificationsCount); err != nil {
		t.Fatalf("query email_verifications: %v", err)
	}
	if verificationsCount != 0 {
		t.Fatalf("email_verifications count=%d want=0", verificationsCount)
	}

	var recordsCount int
	if err := db.QueryRow(context.Background(), `select count(1) from email_records where to_email=$1`, "queue-down@example.com").Scan(&recordsCount); err != nil {
		t.Fatalf("query email_records: %v", err)
	}
	if recordsCount != 0 {
		t.Fatalf("email_records count=%d want=0", recordsCount)
	}
}

func TestRegisterVerifyMagicLinkSameDeviceRedirectsThenLogin(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)
	testutil.FlushRedisKeys(t, rdb, "login_fail:*")

	r := newRegisterRouter(t, db, rdb, 8)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "magic-activate@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", res.Code, http.StatusAccepted, res.Body.String())
	}

	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}
	stateCookie := findCookieByName(res, magicLinkStateCookieName)
	if stateCookie == nil {
		t.Fatalf("%s cookie missing in register response", magicLinkStateCookieName)
	}
	parsedLink, err := url.Parse(job.MagicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	token := parsedLink.Query().Get("token")
	state := parsedLink.Query().Get("state")
	if token == "" || state == "" {
		t.Fatalf("magic link token/state missing: %s", job.MagicLink)
	}
	if stateCookie.Value != state {
		t.Fatalf("cookie state=%q want=%q", stateCookie.Value, state)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/verify-magic-link?token="+url.QueryEscape(token)+"&state="+url.QueryEscape(state), nil)
	req.AddCookie(stateCookie)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("verify-magic-link status=%d want=%d body=%s", w.Code, http.StatusFound, w.Body.String())
	}
	location := w.Header().Get("Location")
	wantLocation := buildMagicLinkSuccessURL("http://auth.example.com")
	if location != wantLocation {
		t.Fatalf("redirect location=%q want=%q", location, wantLocation)
	}

	var status int16
	var emailVerifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `select status,email_verified_at from users where email=$1`, "magic-activate@example.com").Scan(&status, &emailVerifiedAt); err != nil {
		t.Fatalf("query user after magic-link verify: %v", err)
	}
	if status != 1 || emailVerifiedAt == nil {
		t.Fatalf("user status=%d email_verified_at=%v want status=1 email_verified_at!=nil", status, emailVerifiedAt)
	}

	var verifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `
select ev.verified_at
from email_verifications ev
join users u on u.id = ev.user_id
where u.email = $1
  and ev.token_type = 'magic_link'`,
		"magic-activate@example.com",
	).Scan(&verifiedAt); err != nil {
		t.Fatalf("query magic_link verification row: %v", err)
	}
	if verifiedAt == nil {
		t.Fatal("magic_link verification should be marked verified")
	}

	loginRes := performJSONRequest(t, r, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    "magic-activate@example.com",
		"password": "Passw0rd!",
	})
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login status=%d want=%d body=%s", loginRes.Code, http.StatusOK, loginRes.Body.String())
	}
}

func TestVerifyMagicLinkCrossDeviceShowsOTPFallback(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	r := newRegisterRouter(t, db, rdb, 8)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "magic-cross-device@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", res.Code, http.StatusAccepted, res.Body.String())
	}

	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}
	parsedLink, err := url.Parse(job.MagicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	token := parsedLink.Query().Get("token")
	state := parsedLink.Query().Get("state")
	if token == "" || state == "" {
		t.Fatalf("magic link token/state missing: %s", job.MagicLink)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/verify-magic-link?token="+url.QueryEscape(token)+"&state="+url.QueryEscape(state), nil)
	req.AddCookie(&http.Cookie{Name: magicLinkStateCookieName, Value: "different-state"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("verify-magic-link status=%d want=%d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "manually enter the 6-digit OTP") {
		t.Fatalf("fallback page body missing OTP guidance: %s", w.Body.String())
	}

	var status int16
	var emailVerifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `select status,email_verified_at from users where email=$1`, "magic-cross-device@example.com").Scan(&status, &emailVerifiedAt); err != nil {
		t.Fatalf("query user after cross-device verify attempt: %v", err)
	}
	if status != 0 || emailVerifiedAt != nil {
		t.Fatalf("user should remain pending; status=%d email_verified_at=%v", status, emailVerifiedAt)
	}
	var magicVerifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `
select ev.verified_at
from email_verifications ev
join users u on u.id = ev.user_id
where u.email = $1
  and ev.token_type = 'magic_link'`,
		"magic-cross-device@example.com",
	).Scan(&magicVerifiedAt); err != nil {
		t.Fatalf("query magic_link row after cross-device attempt: %v", err)
	}
	if magicVerifiedAt != nil {
		t.Fatalf("magic_link verified_at=%v want=nil", magicVerifiedAt)
	}
}

func TestVerifyMagicLinkExpiredReturnsErrorPage(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	r := newRegisterRouter(t, db, rdb, 8)
	res := performJSONRequest(t, r, http.MethodPost, "/v1/auth/register", map[string]string{
		"email":    "magic-expired@example.com",
		"password": "Passw0rd!",
	})
	if res.Code != http.StatusAccepted {
		t.Fatalf("register status=%d want=%d body=%s", res.Code, http.StatusAccepted, res.Body.String())
	}

	job, err := popQueuedJob(t, rdb)
	if err != nil {
		t.Fatalf("pop queued job: %v", err)
	}
	parsedLink, err := url.Parse(job.MagicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	token := parsedLink.Query().Get("token")
	state := parsedLink.Query().Get("state")
	if token == "" || state == "" {
		t.Fatalf("magic link token/state missing: %s", job.MagicLink)
	}
	stateCookie := findCookieByName(res, magicLinkStateCookieName)
	if stateCookie == nil {
		t.Fatalf("%s cookie missing in register response", magicLinkStateCookieName)
	}

	if _, err := db.Exec(context.Background(), `
update email_verifications ev
set expires_at = now() - interval '1 minute'
from users u
where ev.user_id = u.id
  and u.email = $1
  and ev.token_type = 'magic_link'
  and ev.verified_at is null`, "magic-expired@example.com"); err != nil {
		t.Fatalf("expire magic link row: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/verify-magic-link?token="+url.QueryEscape(token)+"&state="+url.QueryEscape(state), nil)
	req.AddCookie(stateCookie)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("verify-magic-link status=%d want=%d body=%s", w.Code, http.StatusGone, w.Body.String())
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "expired") {
		t.Fatalf("error page should mention expiry: %s", w.Body.String())
	}
	var status int16
	var emailVerifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `select status,email_verified_at from users where email=$1`, "magic-expired@example.com").Scan(&status, &emailVerifiedAt); err != nil {
		t.Fatalf("query user after expired magic-link attempt: %v", err)
	}
	if status != 0 || emailVerifiedAt != nil {
		t.Fatalf("user should remain pending; status=%d email_verified_at=%v", status, emailVerifiedAt)
	}
	var magicVerifiedAt *time.Time
	if err := db.QueryRow(context.Background(), `
select ev.verified_at
from email_verifications ev
join users u on u.id = ev.user_id
where u.email = $1
  and ev.token_type = 'magic_link'`,
		"magic-expired@example.com",
	).Scan(&magicVerifiedAt); err != nil {
		t.Fatalf("query magic_link row after expired attempt: %v", err)
	}
	if magicVerifiedAt != nil {
		t.Fatalf("magic_link verified_at=%v want=nil", magicVerifiedAt)
	}
}

func TestBuildVerificationEmailBodyEscapesHTML(t *testing.T) {
	otp := `12<34>`
	magicLink := `http://example.com/verify?token="><script>alert(1)</script>`
	htmlBody, textBody := buildVerificationEmailBody(otp, magicLink)

	if strings.Contains(htmlBody, `<script>alert(1)</script>`) {
		t.Fatalf("html body should escape script tag: %s", htmlBody)
	}
	if !strings.Contains(htmlBody, `&lt;script&gt;alert(1)&lt;/script&gt;`) {
		t.Fatalf("html body should contain escaped script text: %s", htmlBody)
	}
	if strings.Contains(htmlBody, `<strong>12<34></strong>`) {
		t.Fatalf("html body should escape OTP value: %s", htmlBody)
	}
	if !strings.Contains(htmlBody, `<strong>12&lt;34&gt;</strong>`) {
		t.Fatalf("html body should contain escaped OTP value: %s", htmlBody)
	}

	// Plain-text body remains unescaped intentionally for readability.
	if !strings.Contains(textBody, otp) || !strings.Contains(textBody, magicLink) {
		t.Fatalf("text body should include original OTP and link: %s", textBody)
	}
}

func TestBuildMagicLinkUsesConfiguredPublicBaseURL(t *testing.T) {
	link := buildMagicLink("https://auth.example.com", "abc123", "state123")
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	if parsed.String() == "" || parsed.Path != "/api/v1/auth/verify-magic-link" {
		t.Fatalf("unexpected parsed magic link: %s", parsed.String())
	}
	if parsed.Query().Get("token") != "abc123" || parsed.Query().Get("state") != "state123" {
		t.Fatalf("query token/state mismatch in %s", parsed.String())
	}
}

func TestBuildMagicLinkSupportsBasePathAndIgnoresFragments(t *testing.T) {
	link := buildMagicLink("https://example.com/auth/public?foo=bar#frag", "tok", "st")
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	if parsed.Path != "/auth/public/api/v1/auth/verify-magic-link" {
		t.Fatalf("path=%q want=%q", parsed.Path, "/auth/public/api/v1/auth/verify-magic-link")
	}
	if parsed.Query().Get("token") != "tok" || parsed.Query().Get("state") != "st" {
		t.Fatalf("query token/state mismatch in %s", parsed.String())
	}
}

func TestIsSecureRequestByTLSOrProxyHeaders(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		headers    map[string]string
		wantSecure bool
	}{
		{
			name:       "tls request",
			url:        "https://auth.example.com/path",
			wantSecure: true,
		},
		{
			name: "x-forwarded-proto https",
			url:  "http://auth.example.com/path",
			headers: map[string]string{
				"X-Forwarded-Proto": "https",
			},
			wantSecure: true,
		},
		{
			name: "forwarded proto https",
			url:  "http://auth.example.com/path",
			headers: map[string]string{
				"Forwarded": "for=192.0.2.1;proto=https;by=203.0.113.43",
			},
			wantSecure: true,
		},
		{
			name:       "plain http no proxy headers",
			url:        "http://auth.example.com/path",
			wantSecure: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			c.Request = req

			got := isSecureRequest(c)
			if got != tc.wantSecure {
				t.Fatalf("isSecureRequest()=%v want=%v", got, tc.wantSecure)
			}
		})
	}
}

func popQueuedJob(t *testing.T, rdb *goredis.Client) (queuedEmailJob, error) {
	t.Helper()
	raw, err := rdb.LPop(context.Background(), emailQueueName).Result()
	if err != nil {
		return queuedEmailJob{}, err
	}
	var job queuedEmailJob
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		return queuedEmailJob{}, err
	}
	return job, nil
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func findCookieByName(res *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range res.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func assertVerifyEmailErrorReason(t *testing.T, res *httptest.ResponseRecorder, expectedReason string) {
	t.Helper()
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
		t.Fatalf("code=%d want=%d body=%s", body.Code, errcode.BadRequest, res.Body.String())
	}
	if body.Data.Reason != expectedReason {
		t.Fatalf("reason=%q want=%q body=%s", body.Data.Reason, expectedReason, res.Body.String())
	}
}

func newRegisterRouter(t *testing.T, db *pgxpool.Pool, rdb *goredis.Client, passwordMinLen int) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := newTestAuthHandler(t, db, rdb)
	h.PasswordMinLen = passwordMinLen
	h.BcryptCost = 4
	r.POST("/v1/auth/register", ginmid.Wrap(h.Register))
	r.POST("/v1/auth/verify-email", ginmid.Wrap(h.VerifyEmail))
	r.GET("/v1/auth/verify-magic-link", ginmid.Wrap(h.VerifyMagicLink))
	r.POST("/v1/auth/login", func(c *gin.Context) { c.Request.RemoteAddr = "192.0.2.1:12345"; ginmid.Wrap(h.Login)(c) })
	return r
}
