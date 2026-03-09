package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type mixpanelClient struct {
	token      string
	endpoint   string
	httpClient *http.Client
}

type mixpanelEvent struct {
	Event      string         `json:"event"`
	Properties map[string]any `json:"properties"`
}

type mixpanelResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

func (c *mixpanelClient) Track(ctx context.Context, ev Event) error {
	if strings.TrimSpace(ev.Name) == "" {
		return fmt.Errorf("analytics event name is required")
	}
	ts := ev.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	props := BuildProperties(Event{
		UserID:     ev.UserID,
		Email:      ev.Email,
		Timestamp:  ts,
		Properties: ev.Properties,
	})
	props["token"] = c.token
	if distinctID := firstNonEmpty(strings.TrimSpace(ev.UserID), normalizeEmail(ev.Email)); distinctID != "" {
		props["distinct_id"] = distinctID
	}
	props["time"] = ts.Unix()

	payload, err := json.Marshal([]mixpanelEvent{{Event: ev.Name, Properties: props}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("mixpanel track request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded mixpanelResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil && err != io.EOF {
		return fmt.Errorf("decode mixpanel response: %w", err)
	}
	if decoded.Status != "" && !strings.EqualFold(decoded.Status, "ok") {
		return fmt.Errorf("mixpanel track request rejected: %s", firstNonEmpty(decoded.Error, decoded.Status))
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if s := strings.TrimSpace(value); s != "" {
			return s
		}
	}
	return ""
}
