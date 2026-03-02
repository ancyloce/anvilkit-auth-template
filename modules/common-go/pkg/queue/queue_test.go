package queue

import (
	"errors"
	"testing"
	"time"

	redismock "github.com/go-redis/redismock/v9"
)

type emailJob struct {
	RecordID string `json:"record_id"`
	To       string `json:"to"`
	Priority int    `json:"priority"`
}

func TestNew_ValidatesClient(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrNilRedisClient) {
		t.Fatalf("err=%v want=%v", err, ErrNilRedisClient)
	}
}

func TestEnqueue_UsesRPushWithJSONPayload(t *testing.T) {
	client, mock := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	payload := emailJob{RecordID: "job-1", To: "user@example.com", Priority: 10}
	mock.ExpectRPush("email:send", `{"record_id":"job-1","to":"user@example.com","priority":10}`).SetVal(1)

	if err := q.Enqueue("email:send", payload); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

func TestEnqueue_ReturnsMarshalError(t *testing.T) {
	client, _ := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	badPayload := map[string]any{"fn": func() {}}
	if err := q.Enqueue("email:send", badPayload); err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestEnqueue_AllowsNullPayload(t *testing.T) {
	client, mock := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	mock.ExpectRPush("email:send", "null").SetVal(1)

	if err := q.Enqueue("email:send", nil); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

func TestDequeue_ReturnsRawPayloadFromBLPop(t *testing.T) {
	client, mock := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	raw := `{"record_id":"job-2","to":"ops@example.com","priority":1}`
	mock.ExpectBLPop(5*time.Second, "email:send").SetVal([]string{"email:send", raw})

	payload, ok, err := q.Dequeue("email:send", 5*time.Second)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if !ok {
		t.Fatal("expected payload to be available")
	}
	if string(payload) != raw {
		t.Fatalf("payload=%s want=%s", payload, raw)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

func TestDequeue_TimeoutReturnsNoPayload(t *testing.T) {
	client, mock := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	mock.ExpectBLPop(2*time.Second, "email:send").RedisNil()

	payload, ok, err := q.Dequeue("email:send", 2*time.Second)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if ok {
		t.Fatal("expected no payload on timeout")
	}
	if payload != nil {
		t.Fatalf("payload=%v want=nil", payload)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

func TestDequeue_InvalidBLPopReply(t *testing.T) {
	client, mock := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	mock.ExpectBLPop(time.Second, "email:send").SetVal([]string{"email:send"})

	payload, ok, err := q.Dequeue("email:send", time.Second)
	if !errors.Is(err, ErrInvalidBLPopReply) {
		t.Fatalf("err=%v want=%v", err, ErrInvalidBLPopReply)
	}
	if ok {
		t.Fatal("expected ok=false on invalid BLPOP reply")
	}
	if payload != nil {
		t.Fatalf("payload=%v want=nil", payload)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

func TestDequeueInto_DeserializesJSONPayload(t *testing.T) {
	client, mock := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	raw := `{"record_id":"job-3","to":"team@example.com","priority":5}`
	mock.ExpectBLPop(time.Second, "email:send").SetVal([]string{"email:send", raw})

	var got emailJob
	ok, err := q.DequeueInto("email:send", time.Second, &got)
	if err != nil {
		t.Fatalf("dequeue into: %v", err)
	}
	if !ok {
		t.Fatal("expected payload")
	}
	if got.RecordID != "job-3" || got.To != "team@example.com" || got.Priority != 5 {
		t.Fatalf("unexpected payload: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

func TestDequeueInto_DeserializesMapPayload(t *testing.T) {
	client, mock := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	raw := `{"record_id":"job-4","meta":{"attempt":2},"tags":["verification","email"]}`
	mock.ExpectBLPop(time.Second, "email:send").SetVal([]string{"email:send", raw})

	var got map[string]any
	ok, err := q.DequeueInto("email:send", time.Second, &got)
	if err != nil {
		t.Fatalf("dequeue into map: %v", err)
	}
	if !ok {
		t.Fatal("expected payload")
	}

	recordID, _ := got["record_id"].(string)
	if recordID != "job-4" {
		t.Fatalf("record_id=%q want=job-4", recordID)
	}

	meta, _ := got["meta"].(map[string]any)
	attempt, _ := meta["attempt"].(float64)
	if int(attempt) != 2 {
		t.Fatalf("attempt=%v want=2", attempt)
	}

	tags, _ := got["tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("tags len=%d want=2", len(tags))
	}
	if tags[0] != "verification" || tags[1] != "email" {
		t.Fatalf("unexpected tags: %#v", tags)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

func TestQueueLength_UsesLLen(t *testing.T) {
	client, mock := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	mock.ExpectLLen("email:send").SetVal(7)

	n, err := q.QueueLength("email:send")
	if err != nil {
		t.Fatalf("queue length: %v", err)
	}
	if n != 7 {
		t.Fatalf("length=%d want=7", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

func TestQueueLength_PropagatesRedisError(t *testing.T) {
	client, mock := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	mock.ExpectLLen("email:send").SetErr(errors.New("redis unavailable"))

	if _, err := q.QueueLength("email:send"); err == nil || err.Error() != "redis unavailable" {
		t.Fatalf("err=%v want=%q", err, "redis unavailable")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

func TestMethods_ValidateQueueName(t *testing.T) {
	client, _ := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	if err := q.Enqueue("", map[string]any{"x": 1}); !errors.Is(err, ErrEmptyQueueName) {
		t.Fatalf("enqueue err=%v want=%v", err, ErrEmptyQueueName)
	}
	if _, _, err := q.Dequeue("   ", time.Second); !errors.Is(err, ErrEmptyQueueName) {
		t.Fatalf("dequeue err=%v want=%v", err, ErrEmptyQueueName)
	}
	if _, err := q.QueueLength("\n"); !errors.Is(err, ErrEmptyQueueName) {
		t.Fatalf("queue length err=%v want=%v", err, ErrEmptyQueueName)
	}
}

func TestDequeueInto_ValidatesDestination(t *testing.T) {
	client, _ := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	if _, err := q.DequeueInto("email:send", time.Second, nil); !errors.Is(err, ErrNilDestination) {
		t.Fatalf("err=%v want=%v", err, ErrNilDestination)
	}
}

func TestDequeueInto_ReturnsUnmarshalError(t *testing.T) {
	client, mock := redismock.NewClientMock()
	q, err := New(client)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	mock.ExpectBLPop(time.Second, "email:send").SetVal([]string{"email:send", "not-json"})

	var got emailJob
	if _, err := q.DequeueInto("email:send", time.Second, &got); err == nil {
		t.Fatal("expected unmarshal error")
	}
}
