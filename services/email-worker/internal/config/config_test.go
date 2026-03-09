package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadFromEnvAnalyticsEnabledRequiresToken(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ANALYTICS_ENABLED", "true")
	t.Setenv("MIXPANEL_TOKEN", "")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("LoadFromEnv() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "MIXPANEL_TOKEN") {
		t.Fatalf("LoadFromEnv() error = %q, want mention MIXPANEL_TOKEN", err)
	}
}

func TestLoadFromEnvAnalyticsConfig(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ANALYTICS_ENABLED", "true")
	t.Setenv("MIXPANEL_TOKEN", "mp-token")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if !cfg.Analytics.Enabled {
		t.Fatal("Analytics.Enabled = false, want true")
	}
	if cfg.Analytics.MixpanelToken != "mp-token" {
		t.Fatalf("MixpanelToken = %q, want mp-token", cfg.Analytics.MixpanelToken)
	}
}

func TestLoadFromEnvQueueBacklogPollConfig(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("EMAIL_QUEUE_BACKLOG_POLL_SEC", "30")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.QueuePollInterval != 30*time.Second {
		t.Fatalf("QueuePollInterval = %v, want 30s", cfg.QueuePollInterval)
	}
}

func TestLoadFromEnvQueueBacklogPollConfigRejectsNonPositive(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("EMAIL_QUEUE_BACKLOG_POLL_SEC", "0")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("LoadFromEnv() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "EMAIL_QUEUE_BACKLOG_POLL_SEC") {
		t.Fatalf("LoadFromEnv() error = %q, want mention EMAIL_QUEUE_BACKLOG_POLL_SEC", err)
	}
}

func TestLoadFromEnvMetricsAddrConfig(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("EMAIL_METRICS_ADDR", ":9191")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.MetricsAddr != ":9191" {
		t.Fatalf("MetricsAddr = %q, want :9191", cfg.MetricsAddr)
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("EMAIL_WEBHOOK_SECRET", "secret")
}
