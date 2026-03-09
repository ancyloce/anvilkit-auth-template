package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"anvilkit-auth-template/modules/common-go/pkg/analytics"
	commonqueue "anvilkit-auth-template/modules/common-go/pkg/queue"
	"anvilkit-auth-template/services/email-worker/internal/sender"
	workerstore "anvilkit-auth-template/services/email-worker/internal/store"
	redismock "github.com/go-redis/redismock/v9"
)

type queueResp struct {
	job EmailJob
	ok  bool
	err error
}

type fakeQueue struct {
	mu       sync.Mutex
	resps    []queueResp
	timeouts []time.Duration
	enqueued []EmailJob
}

func (q *fakeQueue) DequeueIntoContext(ctx context.Context, queueName string, timeout time.Duration, out any) (bool, error) {
	q.mu.Lock()
	q.timeouts = append(q.timeouts, timeout)
	if len(q.resps) > 0 {
		resp := q.resps[0]
		q.resps = q.resps[1:]
		q.mu.Unlock()
		if resp.err != nil {
			return false, resp.err
		}
		if resp.ok {
			job, ok := out.(*EmailJob)
			if !ok {
				return false, errors.New("unexpected output type")
			}
			*job = resp.job
		}
		return resp.ok, nil
	}
	q.mu.Unlock()

	<-ctx.Done()
	return false, ctx.Err()
}

func (q *fakeQueue) EnqueueContext(_ context.Context, _ string, payload any) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := payload.(EmailJob)
	if !ok {
		return errors.New("unexpected payload type")
	}
	q.enqueued = append(q.enqueued, job)
	return nil
}

type senderResp struct {
	externalID string
	err        error
}

type fakeSender struct {
	mu       sync.Mutex
	resps    []senderResp
	requests []sender.Request
}

func (s *fakeSender) Send(_ context.Context, req sender.Request) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
	if len(s.resps) == 0 {
		return "", nil
	}
	resp := s.resps[0]
	s.resps = s.resps[1:]
	return resp.externalID, resp.err
}

type fakeStore struct {
	mu      sync.Mutex
	sent    []struct{ recordID, externalID string }
	failed  []struct{ recordID, reason string }
	bounced []struct {
		recordID, reason, bounceType string
		smtpCode, retryCount         int
	}
	blacklisted   map[string]bool
	analyticsByID map[string]*workerstore.AnalyticsRecord
	markSentErr   error
	markFailErr   error
	onMarkSent    func()
	onMarkFailed  func()
}

func (s *fakeStore) MarkSent(_ context.Context, recordID, externalID string) error {
	s.mu.Lock()
	s.sent = append(s.sent, struct{ recordID, externalID string }{recordID: recordID, externalID: externalID})
	cb := s.onMarkSent
	err := s.markSentErr
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
	return err
}

func (s *fakeStore) MarkFailed(_ context.Context, recordID, reason string) error {
	s.mu.Lock()
	s.failed = append(s.failed, struct{ recordID, reason string }{recordID: recordID, reason: reason})
	cb := s.onMarkFailed
	err := s.markFailErr
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
	return err
}

func (s *fakeStore) MarkBounced(_ context.Context, recordID, reason, bounceType string, smtpCode, retryCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bounced = append(s.bounced, struct {
		recordID, reason, bounceType string
		smtpCode, retryCount         int
	}{recordID: recordID, reason: reason, bounceType: bounceType, smtpCode: smtpCode, retryCount: retryCount})
	return nil
}

func (s *fakeStore) Blacklist(_ context.Context, emailAddr, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blacklisted == nil {
		s.blacklisted = map[string]bool{}
	}
	s.blacklisted[strings.ToLower(strings.TrimSpace(emailAddr))] = true
	return nil
}

func (s *fakeStore) IsBlacklisted(_ context.Context, emailAddr string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blacklisted == nil {
		return false, nil
	}
	return s.blacklisted[strings.ToLower(strings.TrimSpace(emailAddr))], nil
}

func (s *fakeStore) LookupAnalyticsRecordByID(_ context.Context, recordID string) (*workerstore.AnalyticsRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.analyticsByID == nil || s.analyticsByID[recordID] == nil {
		return nil, workerstore.ErrEmailRecordNotFound
	}
	record := *s.analyticsByID[recordID]
	return &record, nil
}

type fakeAnalytics struct {
	mu     sync.Mutex
	events []analytics.Event
}

func (f *fakeAnalytics) Track(_ context.Context, event analytics.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	props := make(map[string]any, len(event.Properties))
	for k, v := range event.Properties {
		props[k] = v
	}
	event.Properties = props
	f.events = append(f.events, event)
	return nil
}

func TestRun_DefaultTimeoutAndSuccessPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{
		resps: []queueResp{{
			ok: true,
			job: EmailJob{
				RecordID: "rec-1",
				To:       "user@example.com",
				Subject:  "subject",
				HTMLBody: "<p>hello</p>",
				TextBody: "hello",
			},
		}},
	}
	s := &fakeSender{
		resps: []senderResp{{externalID: "esp-1"}},
	}
	st := &fakeStore{
		onMarkSent: cancel,
	}

	c := &Consumer{
		Queue:     q,
		QueueName: "email:send",
		Timeout:   0,
		Sender:    s,
		Store:     st,
	}

	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(q.timeouts) == 0 || q.timeouts[0] != 5*time.Second {
		t.Fatalf("timeout=%v want=5s", q.timeouts)
	}
	if len(s.requests) != 1 {
		t.Fatalf("send requests=%d want=1", len(s.requests))
	}
	gotReq := s.requests[0]
	if gotReq.To != "user@example.com" || gotReq.Subject != "subject" || gotReq.HTMLBody != "<p>hello</p>" || gotReq.TextBody != "hello" {
		t.Fatalf("unexpected send request: %+v", gotReq)
	}
	if len(st.sent) != 1 {
		t.Fatalf("sent records=%d want=1", len(st.sent))
	}
	if st.sent[0].recordID != "rec-1" || st.sent[0].externalID != "esp-1" {
		t.Fatalf("unexpected sent record: %+v", st.sent[0])
	}
	if len(st.failed) != 0 {
		t.Fatalf("failed records=%d want=0", len(st.failed))
	}
}

func TestRun_TracksVerificationEmailSent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{
		resps: []queueResp{{
			ok: true,
			job: EmailJob{
				RecordID: "rec-analytics-sent",
				To:       "user@example.com",
				Subject:  "subject",
				HTMLBody: "<p>hello</p>",
				TextBody: "hello",
			},
		}},
	}
	s := &fakeSender{resps: []senderResp{{externalID: "esp-sent"}}}
	tracker := &fakeAnalytics{}
	st := &fakeStore{
		analyticsByID: map[string]*workerstore.AnalyticsRecord{
			"rec-analytics-sent": {UserID: "user-1", Email: "user@example.com"},
		},
		onMarkSent: cancel,
	}

	c := &Consumer{
		Queue:     q,
		QueueName: "email:send",
		Timeout:   time.Second,
		Sender:    s,
		Store:     st,
		Analytics: tracker,
	}

	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(tracker.events) != 1 {
		t.Fatalf("event count=%d want=1", len(tracker.events))
	}
	event := tracker.events[0]
	if event.Name != "verification_email_sent" {
		t.Fatalf("event name=%q want verification_email_sent", event.Name)
	}
	if event.UserID != "user-1" || event.Email != "user@example.com" {
		t.Fatalf("unexpected event identity: %+v", event)
	}
	if event.Timestamp.IsZero() {
		t.Fatal("timestamp should be set")
	}
}

func TestRun_SenderFailureMarksFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{
		resps: []queueResp{{
			ok: true,
			job: EmailJob{
				RecordID: "rec-2",
				To:       "user@example.com",
				HTMLBody: "<p>hello</p>",
				TextBody: "hello",
			},
		}},
	}
	s := &fakeSender{
		resps: []senderResp{{err: errors.New("smtp failed")}},
	}
	st := &fakeStore{
		onMarkFailed: cancel,
	}

	c := &Consumer{
		Queue:     q,
		QueueName: "email:send",
		Timeout:   5 * time.Second,
		Sender:    s,
		Store:     st,
	}

	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(st.failed) != 1 {
		t.Fatalf("failed records=%d want=1", len(st.failed))
	}
	if st.failed[0].recordID != "rec-2" {
		t.Fatalf("failed record_id=%q want=rec-2", st.failed[0].recordID)
	}
	if st.failed[0].reason != "smtp failed" {
		t.Fatalf("failed reason=%q want=%q", st.failed[0].reason, "smtp failed")
	}
	if len(st.sent) != 0 {
		t.Fatalf("sent records=%d want=0", len(st.sent))
	}
}

func TestRun_HardBounceTracksBounceType(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	q := &fakeQueue{
		resps: []queueResp{{
			ok: true,
			job: EmailJob{
				RecordID: "rec-hard-bounce",
				To:       "user@example.com",
				HTMLBody: "<p>hello</p>",
				TextBody: "hello",
			},
		}},
	}
	s := &fakeSender{
		resps: []senderResp{{
			err: &sender.DeliveryError{
				Cause: errors.New("smtp 550 mailbox unavailable"),
				Classification: sender.BounceClassification{
					Type:     sender.BounceTypeHard,
					SMTPCode: 550,
				},
			},
		}},
	}
	tracker := &fakeAnalytics{}
	st := &fakeStore{
		analyticsByID: map[string]*workerstore.AnalyticsRecord{
			"rec-hard-bounce": {UserID: "user-2", Email: "user@example.com"},
		},
	}

	c := &Consumer{
		Queue:     q,
		QueueName: "email:send",
		Timeout:   time.Second,
		Sender:    s,
		Store:     st,
		Analytics: tracker,
	}

	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(tracker.events) != 1 {
		t.Fatalf("event count=%d want=1", len(tracker.events))
	}
	event := tracker.events[0]
	if event.Name != "verification_email_bounced" {
		t.Fatalf("event name=%q want verification_email_bounced", event.Name)
	}
	if event.Properties["bounce_type"] != "hard" {
		t.Fatalf("bounce_type=%v want hard", event.Properties["bounce_type"])
	}
	if event.UserID != "user-2" || event.Email != "user@example.com" {
		t.Fatalf("unexpected event identity: %+v", event)
	}
}

func TestRun_SkipsAnalyticsWhenUserIDMissing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{
		resps: []queueResp{{
			ok: true,
			job: EmailJob{
				RecordID: "rec-no-user",
				To:       "user@example.com",
				Subject:  "subject",
				HTMLBody: "<p>hello</p>",
				TextBody: "hello",
			},
		}},
	}
	s := &fakeSender{resps: []senderResp{{externalID: "esp-sent"}}}
	tracker := &fakeAnalytics{}
	st := &fakeStore{
		analyticsByID: map[string]*workerstore.AnalyticsRecord{
			"rec-no-user": {UserID: "", Email: "user@example.com"},
		},
		onMarkSent: cancel,
	}

	c := &Consumer{
		Queue:     q,
		QueueName: "email:send",
		Timeout:   time.Second,
		Sender:    s,
		Store:     st,
		Analytics: tracker,
	}

	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(tracker.events) != 0 {
		t.Fatalf("event count=%d want=0", len(tracker.events))
	}
}

func TestRun_SkipsAnalyticsWhenEmailMissing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{
		resps: []queueResp{{
			ok: true,
			job: EmailJob{
				RecordID: "rec-no-email",
				To:       "user@example.com",
				Subject:  "subject",
				HTMLBody: "<p>hello</p>",
				TextBody: "hello",
			},
		}},
	}
	s := &fakeSender{resps: []senderResp{{externalID: "esp-sent"}}}
	tracker := &fakeAnalytics{}
	st := &fakeStore{
		analyticsByID: map[string]*workerstore.AnalyticsRecord{
			"rec-no-email": {UserID: "user-1", Email: ""},
		},
		onMarkSent: cancel,
	}

	c := &Consumer{
		Queue:     q,
		QueueName: "email:send",
		Timeout:   time.Second,
		Sender:    s,
		Store:     st,
		Analytics: tracker,
	}

	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(tracker.events) != 0 {
		t.Fatalf("event count=%d want=0", len(tracker.events))
	}
}

func TestRun_InvalidRecipientMarksFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{
		resps: []queueResp{{
			ok: true,
			job: EmailJob{
				RecordID: "rec-3",
			},
		}},
	}
	s := &fakeSender{}
	st := &fakeStore{
		onMarkFailed: cancel,
	}

	c := &Consumer{
		Queue:     q,
		QueueName: "email:send",
		Timeout:   5 * time.Second,
		Sender:    s,
		Store:     st,
	}

	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(s.requests) != 0 {
		t.Fatalf("send requests=%d want=0", len(s.requests))
	}
	if len(st.failed) != 1 {
		t.Fatalf("failed records=%d want=1", len(st.failed))
	}
	if st.failed[0].recordID != "rec-3" {
		t.Fatalf("failed record_id=%q want=rec-3", st.failed[0].recordID)
	}
	if st.failed[0].reason == "" {
		t.Fatal("failed reason should not be empty")
	}
}

func TestRun_RendersVerificationTemplatesWhenBodiesMissing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{
		resps: []queueResp{{
			ok: true,
			job: EmailJob{
				RecordID:  "rec-render-1",
				To:        "user@example.com",
				Subject:   "Verify your email",
				OTP:       "123456",
				MagicLink: "https://example.com/verify?token=t&state=s",
				ExpiresIn: "15 minutes",
			},
		}},
	}
	s := &fakeSender{resps: []senderResp{{externalID: "esp-render-1"}}}
	st := &fakeStore{onMarkSent: cancel}

	c := &Consumer{Queue: q, QueueName: "email:send", Timeout: 5 * time.Second, Sender: s, Store: st}
	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(s.requests) != 1 {
		t.Fatalf("send requests=%d want=1", len(s.requests))
	}
	req := s.requests[0]
	if req.HTMLBody == "" || req.TextBody == "" {
		t.Fatalf("expected rendered html/text bodies, got html=%q text=%q", req.HTMLBody, req.TextBody)
	}
	if !containsAll(req.TextBody, "123456", "15 minutes", "https://example.com/verify?token=t&state=s") {
		t.Fatalf("text body missing rendered values: %q", req.TextBody)
	}
	if !containsAll(req.HTMLBody, "123456", "15 minutes", "https://example.com/verify?token=t&amp;state=s") {
		t.Fatalf("html body missing rendered values: %q", req.HTMLBody)
	}
}

func TestRun_MissingTemplateVariablesMarksFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{
		resps: []queueResp{{
			ok: true,
			job: EmailJob{
				RecordID:  "rec-render-2",
				To:        "user@example.com",
				Subject:   "Verify your email",
				OTP:       "123456",
				MagicLink: "",
				ExpiresIn: "15 minutes",
			},
		}},
	}
	s := &fakeSender{}
	st := &fakeStore{onMarkFailed: cancel}

	c := &Consumer{Queue: q, QueueName: "email:send", Timeout: 5 * time.Second, Sender: s, Store: st}
	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(s.requests) != 0 {
		t.Fatalf("send requests=%d want=0", len(s.requests))
	}
	if len(st.failed) != 1 {
		t.Fatalf("failed records=%d want=1", len(st.failed))
	}
	if !containsAll(st.failed[0].reason, ErrInvalidJob.Error(), ErrEmptyMagic.Error()) {
		t.Fatalf("unexpected fail reason=%q", st.failed[0].reason)
	}
}

func TestRun_DecodeErrorIsDroppedAndLoopContinues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{
		resps: []queueResp{
			{err: &json.SyntaxError{Offset: 1}},
			{
				ok: true,
				job: EmailJob{
					RecordID: "rec-4",
					To:       "user@example.com",
					HTMLBody: "<p>hello</p>",
					TextBody: "hello",
				},
			},
		},
	}
	s := &fakeSender{
		resps: []senderResp{{externalID: "esp-4"}},
	}
	st := &fakeStore{
		onMarkSent: cancel,
	}

	c := &Consumer{
		Queue:     q,
		QueueName: "email:send",
		Timeout:   5 * time.Second,
		Sender:    s,
		Store:     st,
	}

	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(s.requests) != 1 {
		t.Fatalf("send requests=%d want=1", len(s.requests))
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func TestRun_StopsGracefullyOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q := &fakeQueue{}
	c := &Consumer{
		Queue:     q,
		QueueName: "email:send",
		Timeout:   5 * time.Second,
		Sender:    &fakeSender{},
		Store:     &fakeStore{},
	}

	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not stop after context cancellation")
	}
}

func TestRun_WithRedisQueue_SuccessPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	redisClient, redisMock := redismock.NewClientMock()
	q, err := commonqueue.New(redisClient)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	raw := `{"record_id":"rec-redis-1","to":"user@example.com","subject":"subject","html_body":"<p>hello</p>","text_body":"hello"}`
	redisMock.ExpectBLPop(5*time.Second, "email:send").SetVal([]string{"email:send", raw})

	s := &fakeSender{
		resps: []senderResp{{externalID: "esp-redis-1"}},
	}
	st := &fakeStore{
		onMarkSent: cancel,
	}

	c := &Consumer{
		Queue:     q,
		QueueName: "email:send",
		Timeout:   5 * time.Second,
		Sender:    s,
		Store:     st,
	}

	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := redisMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
	if len(s.requests) != 1 {
		t.Fatalf("send requests=%d want=1", len(s.requests))
	}
	if len(st.sent) != 1 || st.sent[0].recordID != "rec-redis-1" || st.sent[0].externalID != "esp-redis-1" {
		t.Fatalf("sent=%+v want record_id=rec-redis-1 external_id=esp-redis-1", st.sent)
	}
	if len(st.failed) != 0 {
		t.Fatalf("failed records=%d want=0", len(st.failed))
	}
}

func TestRun_WithRedisQueue_InvalidJSONDroppedThenProcessesNextJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	redisClient, redisMock := redismock.NewClientMock()
	q, err := commonqueue.New(redisClient)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	redisMock.ExpectBLPop(5*time.Second, "email:send").SetVal([]string{"email:send", "not-json"})
	redisMock.ExpectBLPop(5*time.Second, "email:send").SetVal([]string{"email:send", `{"record_id":"rec-redis-2","to":"team@example.com","html_body":"<p>hello</p>","text_body":"hello"}`})

	s := &fakeSender{
		resps: []senderResp{{externalID: "esp-redis-2"}},
	}
	st := &fakeStore{
		onMarkSent: cancel,
	}

	c := &Consumer{
		Queue:     q,
		QueueName: "email:send",
		Timeout:   5 * time.Second,
		Sender:    s,
		Store:     st,
	}

	if err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := redisMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
	if len(s.requests) != 1 {
		t.Fatalf("send requests=%d want=1", len(s.requests))
	}
	if len(st.sent) != 1 || st.sent[0].recordID != "rec-redis-2" {
		t.Fatalf("sent=%+v want record_id=rec-redis-2", st.sent)
	}
}

type fakeScheduler struct {
	delays  []time.Duration
	autoRun bool
}

func (s *fakeScheduler) AfterFunc(d time.Duration, f func()) {
	s.delays = append(s.delays, d)
	if s.autoRun {
		f()
	}
}

func TestRun_HardBounceBlacklistsAndNoRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{resps: []queueResp{{ok: true, job: EmailJob{RecordID: "rec-hard", To: "user@example.com", HTMLBody: "<p>h</p>", TextBody: "h"}}}}
	s := &fakeSender{resps: []senderResp{{err: &sender.DeliveryError{Cause: errors.New("550 mailbox unavailable"), Classification: sender.BounceClassification{Type: sender.BounceTypeHard, SMTPCode: 550}}}}}
	st := &fakeStore{}

	c := &Consumer{Queue: q, QueueName: "email:send", Timeout: time.Second, Sender: s, Store: st, Scheduler: &fakeScheduler{autoRun: true}}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_ = c.Run(ctx)

	if len(st.bounced) != 1 || st.bounced[0].bounceType != "hard" || st.bounced[0].smtpCode != 550 {
		t.Fatalf("unexpected bounced=%+v", st.bounced)
	}
	if !st.blacklisted["user@example.com"] {
		t.Fatalf("expected address blacklisted: %+v", st.blacklisted)
	}
	if len(q.enqueued) != 0 {
		t.Fatalf("unexpected retries enqueued=%d", len(q.enqueued))
	}
}

func TestRun_SoftBounceRetriesWithExpectedIntervals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{resps: []queueResp{{ok: true, job: EmailJob{RecordID: "rec-soft", To: "user@example.com", HTMLBody: "<p>h</p>", TextBody: "h"}}}}
	s := &fakeSender{resps: []senderResp{{err: &sender.DeliveryError{Cause: errors.New("451 mailbox busy"), Classification: sender.BounceClassification{Type: sender.BounceTypeSoft, SMTPCode: 451}}}}}
	st := &fakeStore{}
	sch := &fakeScheduler{autoRun: true}

	c := &Consumer{Queue: q, QueueName: "email:send", Timeout: time.Second, Sender: s, Store: st, Scheduler: sch}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_ = c.Run(ctx)

	if len(st.bounced) != 1 || st.bounced[0].bounceType != "soft" {
		t.Fatalf("unexpected bounced=%+v", st.bounced)
	}
	if len(sch.delays) != 1 || sch.delays[0] != time.Hour {
		t.Fatalf("delays=%v want=[1h]", sch.delays)
	}
	if len(q.enqueued) != 1 || q.enqueued[0].RetryCount != 1 {
		t.Fatalf("enqueued=%+v want retry_count=1", q.enqueued)
	}
}

func TestHandleDeliveryError_SoftBounceScheduledReturnsNil(t *testing.T) {
	q := &fakeQueue{}
	c := &Consumer{Store: &fakeStore{}, Queue: q, QueueName: "email:send", Scheduler: &fakeScheduler{autoRun: true}}
	err := c.handleDeliveryError(
		context.Background(),
		EmailJob{RecordID: "rec-soft-nil", To: "user@example.com", RetryCount: 0},
		&sender.DeliveryError{Cause: errors.New("451 mailbox busy"), Classification: sender.BounceClassification{Type: sender.BounceTypeSoft, SMTPCode: 451}},
	)
	if err != nil {
		t.Fatalf("err=%v want nil", err)
	}
	if len(q.enqueued) != 1 || q.enqueued[0].RetryCount != 1 {
		t.Fatalf("enqueued=%+v want one retry with retry_count=1", q.enqueued)
	}
}

func TestHandleDeliveryError_SoftBounceRetryExhaustedAfterThreeRetries(t *testing.T) {
	c := &Consumer{Store: &fakeStore{}, Queue: &fakeQueue{}, QueueName: "email:send", Scheduler: &fakeScheduler{}}
	err := c.handleDeliveryError(context.Background(), EmailJob{RecordID: "rec-soft-exhausted", To: "user@example.com", RetryCount: 3}, &sender.DeliveryError{Cause: errors.New("451 mailbox busy"), Classification: sender.BounceClassification{Type: sender.BounceTypeSoft, SMTPCode: 451}})
	if !errors.Is(err, ErrSoftBounceExceeded) {
		t.Fatalf("err=%v want=%v", err, ErrSoftBounceExceeded)
	}
}

func TestRun_BlacklistedRecipientIsNotSent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := &fakeQueue{resps: []queueResp{{ok: true, job: EmailJob{RecordID: "rec-bl", To: "user@example.com", HTMLBody: "<p>h</p>", TextBody: "h"}}}}
	s := &fakeSender{}
	st := &fakeStore{blacklisted: map[string]bool{"user@example.com": true}, onMarkFailed: cancel}
	c := &Consumer{Queue: q, QueueName: "email:send", Timeout: time.Second, Sender: s, Store: st}
	_ = c.Run(ctx)

	if len(s.requests) != 0 {
		t.Fatalf("send requests=%d want=0", len(s.requests))
	}
	if len(st.failed) != 1 || st.failed[0].reason != ErrEmailBlacklisted.Error() {
		t.Fatalf("failed=%+v", st.failed)
	}
}
