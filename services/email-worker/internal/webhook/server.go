package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"anvilkit-auth-template/modules/common-go/pkg/analytics"
	workerstore "anvilkit-auth-template/services/email-worker/internal/store"
)

const signatureHeader = "X-ESP-Signature"

type Store interface {
	UpsertWebhookStatusByExternalID(ctx context.Context, externalID, status, message, eventID string, meta map[string]any) (bool, error)
	LookupAnalyticsRecordByExternalID(ctx context.Context, externalID string) (*workerstore.AnalyticsRecord, error)
}

type Server struct {
	Store     Store
	Secret    string
	Analytics analytics.Client
	Metrics   http.Handler
}

type callbackPayload struct {
	ExternalID string         `json:"external_id"`
	Event      string         `json:"event"`
	EventID    string         `json:"event_id"`
	Message    string         `json:"message"`
	Meta       map[string]any `json:"meta"`
}

func NewHandler(s Server) (http.Handler, error) {
	if s.Store == nil {
		return nil, errors.New("nil_store")
	}
	if strings.TrimSpace(s.Secret) == "" {
		return nil, errors.New("empty_webhook_secret")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if s.Metrics != nil {
		mux.Handle("/metrics", s.Metrics)
	}
	mux.HandleFunc("/webhooks/email-status", s.handleEmailStatus)
	return mux, nil
}

func (s Server) handleEmailStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !verifySignature(s.Secret, r.Header.Get(signatureHeader), body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var payload callbackPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json payload", http.StatusBadRequest)
		return
	}
	payload.Event = strings.TrimSpace(strings.ToLower(payload.Event))
	payload.ExternalID = strings.TrimSpace(payload.ExternalID)
	payload.EventID = strings.TrimSpace(payload.EventID)
	if payload.ExternalID == "" || payload.EventID == "" || !allowedEvent(payload.Event) {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	if payload.Meta == nil {
		payload.Meta = map[string]any{}
	}
	inserted, err := s.Store.UpsertWebhookStatusByExternalID(r.Context(), payload.ExternalID, payload.Event, payload.Message, payload.EventID, payload.Meta)
	if err != nil {
		if errors.Is(err, workerstore.ErrEmailRecordNotFound) {
			http.Error(w, "email record not found", http.StatusNotFound)
			return
		}
		log.Printf("email-worker webhook: failed to persist event=%q external_id=%q err=%v", payload.Event, payload.ExternalID, err)
		http.Error(w, "failed to process webhook", http.StatusInternalServerError)
		return
	}

	log.Printf("email-worker webhook: accepted event=%q external_id=%q", payload.Event, payload.ExternalID)
	if inserted && payload.Event == "bounced" {
		s.trackRecordEvent(r.Context(), payload.ExternalID, "verification_email_bounced", map[string]any{
			"bounce_type": sanitizeBounceType(payload.Meta["bounce_type"]),
		})
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func allowedEvent(event string) bool {
	switch event {
	case "delivered", "opened", "clicked", "bounced":
		return true
	default:
		return false
	}
}

func verifySignature(secret, provided string, body []byte) bool {
	secret = strings.TrimSpace(secret)
	provided = strings.TrimSpace(provided)
	if secret == "" || provided == "" {
		return false
	}
	provided = strings.TrimPrefix(strings.ToLower(provided), "sha256=")
	sig, err := hex.DecodeString(provided)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	if len(sig) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare(sig, expected) == 1
}

func (s Server) trackRecordEvent(ctx context.Context, externalID, eventName string, props map[string]any) {
	if s.Analytics == nil {
		return
	}
	record, err := s.Store.LookupAnalyticsRecordByExternalID(ctx, externalID)
	if err != nil {
		log.Printf("email-worker analytics: lookup external_id=%q failed: %v", externalID, err)
		return
	}
	if strings.TrimSpace(record.UserID) == "" {
		log.Printf("email-worker analytics: skip event=%q external_id=%q missing user_id", eventName, externalID)
		return
	}
	if strings.TrimSpace(record.Email) == "" {
		log.Printf("email-worker analytics: skip event=%q external_id=%q missing email", eventName, externalID)
		return
	}
	if err := s.Analytics.Track(ctx, analytics.Event{
		Name:       eventName,
		UserID:     record.UserID,
		Email:      record.Email,
		Timestamp:  time.Now().UTC(),
		Properties: props,
	}); err != nil {
		log.Printf("email-worker analytics: track event=%q external_id=%q failed: %v", eventName, externalID, err)
	}
}

func sanitizeBounceType(raw any) string {
	value, ok := raw.(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return strings.TrimSpace(strings.ToLower(value))
}
