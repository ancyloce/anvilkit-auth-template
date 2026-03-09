package config

import (
	"strings"
	"testing"
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

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("EMAIL_WEBHOOK_SECRET", "secret")
}
