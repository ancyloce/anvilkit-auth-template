package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	commonqueue "anvilkit-auth-template/modules/common-go/pkg/queue"
	"anvilkit-auth-template/services/email-worker/internal/sender"
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
	mu           sync.Mutex
	sent         []struct{ recordID, externalID string }
	failed       []struct{ recordID, reason string }
	markSentErr  error
	markFailErr  error
	onMarkSent   func()
	onMarkFailed func()
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
	if !containsAll(req.TextBody, "123456", "15 minutes", "https://example.com/verify?token=t&amp;state=s") {
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
