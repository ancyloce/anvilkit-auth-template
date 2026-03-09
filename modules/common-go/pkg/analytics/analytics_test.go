package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClientDisabledReturnsNoop(t *testing.T) {
	client, err := NewClient(Config{Enabled: false})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := client.Track(context.Background(), Event{Name: "ignored"}); err != nil {
		t.Fatalf("Track() error = %v", err)
	}
}

func TestNewClientEnabledRequiresToken(t *testing.T) {
	if _, err := NewClient(Config{Enabled: true}); err == nil {
		t.Fatal("NewClient() error = nil, want error")
	}
}

func TestMixpanelClientTrackSendsRequiredProperties(t *testing.T) {
	var got []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("content-type = %q, want application/json", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{Enabled: true, MixpanelToken: "mp-token", Endpoint: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ts := time.Date(2026, 3, 9, 10, 11, 12, 0, time.UTC)
	err = client.Track(context.Background(), Event{
		Name:      "account_activated",
		UserID:    "user-1",
		Email:     "User@example.com",
		Timestamp: ts,
		Properties: map[string]any{
			"method": "otp",
		},
	})
	if err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("payload count = %d, want 1", len(got))
	}
	props, ok := got[0]["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties type = %T", got[0]["properties"])
	}
	if got[0]["event"] != "account_activated" {
		t.Fatalf("event = %v, want account_activated", got[0]["event"])
	}
	if props["token"] != "mp-token" {
		t.Fatalf("token = %v, want mp-token", props["token"])
	}
	if props["distinct_id"] != "user-1" {
		t.Fatalf("distinct_id = %v, want user-1", props["distinct_id"])
	}
	if props["user_id"] != "user-1" {
		t.Fatalf("user_id = %v, want user-1", props["user_id"])
	}
	if props["email"] != "user@example.com" {
		t.Fatalf("email = %v, want user@example.com", props["email"])
	}
	if props["timestamp"] != "2026-03-09T10:11:12Z" {
		t.Fatalf("timestamp = %v", props["timestamp"])
	}
	if props["method"] != "otp" {
		t.Fatalf("method = %v, want otp", props["method"])
	}
	if props["time"] != float64(ts.Unix()) {
		t.Fatalf("time = %v, want %d", props["time"], ts.Unix())
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("ANALYTICS_ENABLED", "true")
	t.Setenv("MIXPANEL_TOKEN", "token-123")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if cfg.MixpanelToken != "token-123" {
		t.Fatalf("MixpanelToken = %q, want token-123", cfg.MixpanelToken)
	}
}
