package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

type statusCall struct {
	externalID string
	status     string
	eventID    string
}

type fakeStore struct {
	calls []statusCall
	err   error
}

func (f *fakeStore) UpsertWebhookStatusByExternalID(_ context.Context, externalID, status, _ string, eventID string, _ map[string]any) error {
	f.calls = append(f.calls, statusCall{externalID: externalID, status: status, eventID: eventID})
	return f.err
}

func TestEmailStatusWebhook_ValidSignatureProcessesDeliveredAndOpened(t *testing.T) {
	store := &fakeStore{}
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
	store := &fakeStore{}
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
	store := &fakeStore{}
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
	store := &fakeStore{}
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
func sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
