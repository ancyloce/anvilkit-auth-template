package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"anvilkit-auth-template/modules/common-go/pkg/analytics"
	workerstore "anvilkit-auth-template/services/email-worker/internal/store"
)

type statusCall struct {
	externalID string
	status     string
	eventID    string
}

type fakeStore struct {
	calls       []statusCall
	err         error
	analyticsBy map[string]*workerstore.AnalyticsRecord
	inserted    bool
}

func (f *fakeStore) UpsertWebhookStatusByExternalID(_ context.Context, externalID, status, _ string, eventID string, _ map[string]any) (bool, error) {
	f.calls = append(f.calls, statusCall{externalID: externalID, status: status, eventID: eventID})
	return f.inserted, f.err
}

func (f *fakeStore) LookupAnalyticsRecordByExternalID(_ context.Context, externalID string) (*workerstore.AnalyticsRecord, error) {
	if f.analyticsBy == nil || f.analyticsBy[externalID] == nil {
		return nil, workerstore.ErrEmailRecordNotFound
	}
	record := *f.analyticsBy[externalID]
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

func TestEmailStatusWebhook_ValidSignatureProcessesDeliveredAndOpened(t *testing.T) {
	store := &fakeStore{inserted: true}
	h, err := NewHandler(Server{Store: store, Secret: "secret"})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	for _, payload := range []string{
		`{"external_id":"esp-123","event":"delivered","event_id":"evt-1"}`,
		`{"external_id":"esp-123","event":"opened","event_id":"evt-2"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/email-status", bytes.NewBufferString(payload))
		req.Header.Set(signatureHeader, sign("secret", []byte(payload)))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
	}

	if len(store.calls) != 2 {
		t.Fatalf("calls=%d want=2", len(store.calls))
	}
	if store.calls[0].externalID != "esp-123" || store.calls[0].status != "delivered" {
		t.Fatalf("first call=%+v", store.calls[0])
	}
	if store.calls[1].status != "opened" {
		t.Fatalf("second call=%+v", store.calls[1])
	}
}

func TestEmailStatusWebhook_InvalidSignatureReturnsUnauthorized(t *testing.T) {
	store := &fakeStore{inserted: true}
	h, err := NewHandler(Server{Store: store, Secret: "secret"})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	payload := `{"external_id":"esp-123","event":"delivered"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/email-status", bytes.NewBufferString(payload))
	req.Header.Set(signatureHeader, "sha256=deadbeef")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(store.calls) != 0 {
		t.Fatalf("store calls=%d want=0", len(store.calls))
	}
}

func TestEmailStatusWebhook_ExternalIDRequired(t *testing.T) {
	store := &fakeStore{inserted: true}
	h, err := NewHandler(Server{Store: store, Secret: "secret"})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	payload := `{"event":"clicked","event_id":"evt-1"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/email-status", bytes.NewBufferString(payload))
	req.Header.Set(signatureHeader, sign("secret", []byte(payload)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestEmailStatusWebhook_InvalidEventTypeReturnsBadRequest(t *testing.T) {
	store := &fakeStore{inserted: true}
	h, err := NewHandler(Server{Store: store, Secret: "secret"})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	payload := `{"external_id":"esp-123","event":"spam","event_id":"evt-1"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/email-status", bytes.NewBufferString(payload))
	req.Header.Set(signatureHeader, sign("secret", []byte(payload)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(store.calls) != 0 {
		t.Fatalf("store calls=%d want=0", len(store.calls))
	}
}

func TestEmailStatusWebhook_BouncedTracksAnalytics(t *testing.T) {
	store := &fakeStore{
		analyticsBy: map[string]*workerstore.AnalyticsRecord{
			"esp-123": {UserID: "user-1", Email: "user@example.com", SentAt: ptrTime(time.Now().Add(-time.Minute))},
		},
		inserted: true,
	}
	tracker := &fakeAnalytics{}
	h, err := NewHandler(Server{Store: store, Secret: "secret", Analytics: tracker})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	payload := `{"external_id":"esp-123","event":"bounced","event_id":"evt-3","meta":{"bounce_type":"soft"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/email-status", bytes.NewBufferString(payload))
	req.Header.Set(signatureHeader, sign("secret", []byte(payload)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(tracker.events) != 1 {
		t.Fatalf("event count=%d want=1", len(tracker.events))
	}
	event := tracker.events[0]
	if event.Name != "verification_email_bounced" {
		t.Fatalf("event name=%q want verification_email_bounced", event.Name)
	}
	if event.UserID != "user-1" || event.Email != "user@example.com" {
		t.Fatalf("unexpected event identity: %+v", event)
	}
	if event.Properties["bounce_type"] != "soft" {
		t.Fatalf("bounce_type=%v want soft", event.Properties["bounce_type"])
	}
	if event.Timestamp.IsZero() {
		t.Fatal("timestamp should be set")
	}
}

func TestEmailStatusWebhook_DuplicateBounceDoesNotTrackAnalytics(t *testing.T) {
	store := &fakeStore{
		analyticsBy: map[string]*workerstore.AnalyticsRecord{
			"esp-123": {UserID: "user-1", Email: "user@example.com"},
		},
		inserted: false,
	}
	tracker := &fakeAnalytics{}
	h, err := NewHandler(Server{Store: store, Secret: "secret", Analytics: tracker})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	payload := `{"external_id":"esp-123","event":"bounced","event_id":"evt-dup","meta":{"bounce_type":"soft"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/email-status", bytes.NewBufferString(payload))
	req.Header.Set(signatureHeader, sign("secret", []byte(payload)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(store.calls) != 1 {
		t.Fatalf("calls=%d want=1", len(store.calls))
	}
	if len(tracker.events) != 0 {
		t.Fatalf("event count=%d want=0", len(tracker.events))
	}
}

func TestEmailStatusWebhook_SkipsAnalyticsWhenUserIDMissing(t *testing.T) {
	store := &fakeStore{
		analyticsBy: map[string]*workerstore.AnalyticsRecord{
			"esp-123": {UserID: "", Email: "user@example.com"},
		},
		inserted: true,
	}
	tracker := &fakeAnalytics{}
	h, err := NewHandler(Server{Store: store, Secret: "secret", Analytics: tracker})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	payload := `{"external_id":"esp-123","event":"bounced","event_id":"evt-4","meta":{"bounce_type":"hard"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/email-status", bytes.NewBufferString(payload))
	req.Header.Set(signatureHeader, sign("secret", []byte(payload)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(tracker.events) != 0 {
		t.Fatalf("event count=%d want=0", len(tracker.events))
	}
}

func TestEmailStatusWebhook_SkipsAnalyticsWhenEmailMissing(t *testing.T) {
	store := &fakeStore{
		analyticsBy: map[string]*workerstore.AnalyticsRecord{
			"esp-123": {UserID: "user-1", Email: ""},
		},
		inserted: true,
	}
	tracker := &fakeAnalytics{}
	h, err := NewHandler(Server{Store: store, Secret: "secret", Analytics: tracker})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	payload := `{"external_id":"esp-123","event":"bounced","event_id":"evt-5","meta":{"bounce_type":"hard"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/email-status", bytes.NewBufferString(payload))
	req.Header.Set(signatureHeader, sign("secret", []byte(payload)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(tracker.events) != 0 {
		t.Fatalf("event count=%d want=0", len(tracker.events))
	}
}

func sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func ptrTime(ts time.Time) *time.Time { return &ts }
