package handler

import (
	"context"
	"encoding/json"
	"net/http"
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
	"anvilkit-auth-template/services/auth-api/internal/store"
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

	r := newRegisterRouter(db, rdb, 8)
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
	if job.MagicLink == "" || !regexp.MustCompile(`/api/v1/auth/verify-magic-link\?token=`).MatchString(job.MagicLink) {
		t.Fatalf("job magic_link=%q missing verify endpoint/token", job.MagicLink)
	}
	if !containsAll(job.TextBody, job.OTP, job.MagicLink) {
		t.Fatalf("text_body missing OTP or magic link: %q", job.TextBody)
	}
	if !containsAll(job.HTMLBody, job.OTP, job.MagicLink) {
		t.Fatalf("html_body missing OTP or magic link: %q", job.HTMLBody)
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	db := newTestDB(t)
	rdb := newTestRedis(t)
	testutil.TruncateAuthTables(t, db)
	testutil.FlushRedisKeys(t, rdb, emailQueueName)

	r := newRegisterRouter(db, rdb, 8)
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

	r := newRegisterRouter(db, rdb, 10)
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

func newRegisterRouter(db *pgxpool.Pool, rdb *goredis.Client, passwordMinLen int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ginmid.RequestID(), ginmid.ErrorHandler())
	h := &Handler{Store: &store.Store{DB: db}, Redis: rdb, PasswordMinLen: passwordMinLen, BcryptCost: 4}
	r.POST("/v1/auth/register", ginmid.Wrap(h.Register))
	return r
}
