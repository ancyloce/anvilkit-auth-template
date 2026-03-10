package monitoring

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeQueueLengthReader struct {
	backlog int64
	err     error
}

func (f fakeQueueLengthReader) QueueLengthContext(_ context.Context, _ string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.backlog, nil
}

func closeBody(t *testing.T, body io.Closer) {
	t.Helper()
	if err := body.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestMetricsHandlerExportsRequiredEmailMetrics(t *testing.T) {
	metrics, err := NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}

	metrics.ObserveSendSuccess(120 * time.Millisecond)
	metrics.ObserveSendFailure(450 * time.Millisecond)
	metrics.SetQueueBacklog("email:send", 42)

	server := httptest.NewServer(metrics.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer closeBody(t, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want=200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	text := string(body)
	for _, metricName := range []string{
		"email_worker_send_attempts_total",
		"email_worker_send_latency_seconds",
		"email_worker_queue_backlog",
	} {
		if !strings.Contains(text, metricName) {
			t.Fatalf("metrics body missing %q\n%s", metricName, text)
		}
	}
}

func TestQueueBacklogCollectorPollsAndUpdatesGauge(t *testing.T) {
	metrics, err := NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}

	collector := &QueueBacklogCollector{
		Queue:     fakeQueueLengthReader{backlog: 7},
		QueueName: "email:send",
		Metrics:   metrics,
	}

	collector.poll(context.Background())

	server := httptest.NewServer(metrics.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer closeBody(t, resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !strings.Contains(string(body), `email_worker_queue_backlog{queue="email:send"} 7`) {
		t.Fatalf("metrics body missing backlog value:\n%s", string(body))
	}
}

func TestQueueBacklogCollectorRecordsPollFailure(t *testing.T) {
	metrics, err := NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}

	collector := &QueueBacklogCollector{
		Queue:     fakeQueueLengthReader{err: errors.New("redis unavailable")},
		QueueName: "email:send",
		Metrics:   metrics,
	}

	collector.poll(context.Background())

	server := httptest.NewServer(metrics.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer closeBody(t, resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !strings.Contains(string(body), `email_worker_queue_backlog_poll_failures_total{queue="email:send"} 1`) {
		t.Fatalf("metrics body missing poll failure counter:\n%s", string(body))
	}
}
