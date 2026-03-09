package analytics

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"anvilkit-auth-template/modules/common-go/pkg/cfg"
)

const (
	defaultEndpoint = "https://api.mixpanel.com/track"
	timestampFormat = time.RFC3339Nano
)

type Event struct {
	Name       string
	UserID     string
	Email      string
	Timestamp  time.Time
	Properties map[string]any
}

type Client interface {
	Track(context.Context, Event) error
}

type Config struct {
	Enabled       bool
	MixpanelToken string
	Endpoint      string
	HTTPClient    *http.Client
}

func LoadConfigFromEnv() (Config, error) {
	cfg := Config{
		Enabled:       cfg.GetBool("ANALYTICS_ENABLED", false),
		MixpanelToken: strings.TrimSpace(cfg.GetString("MIXPANEL_TOKEN", "")),
		Endpoint:      strings.TrimSpace(cfg.GetString("MIXPANEL_API_ENDPOINT", defaultEndpoint)),
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	if cfg.MixpanelToken == "" {
		return Config{}, fmt.Errorf("MIXPANEL_TOKEN is required when ANALYTICS_ENABLED=true")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultEndpoint
	}
	return cfg, nil
}

func NewClient(cfg Config) (Client, error) {
	if !cfg.Enabled {
		return NoopClient{}, nil
	}
	if strings.TrimSpace(cfg.MixpanelToken) == "" {
		return nil, fmt.Errorf("MIXPANEL_TOKEN is required when analytics is enabled")
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &mixpanelClient{
		token:      cfg.MixpanelToken,
		endpoint:   endpoint,
		httpClient: httpClient,
	}, nil
}

func FormatTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(timestampFormat)
}

func BuildProperties(ev Event) map[string]any {
	ts := ev.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	props := make(map[string]any, len(ev.Properties)+3)
	for k, v := range ev.Properties {
		props[k] = v
	}
	if strings.TrimSpace(ev.UserID) != "" {
		props["user_id"] = strings.TrimSpace(ev.UserID)
	}
	if strings.TrimSpace(ev.Email) != "" {
		props["email"] = strings.TrimSpace(strings.ToLower(ev.Email))
	}
	props["timestamp"] = FormatTimestamp(ts)
	return props
}

type NoopClient struct{}

func (NoopClient) Track(context.Context, Event) error { return nil }
