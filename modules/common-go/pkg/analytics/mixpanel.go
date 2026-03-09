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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("read mixpanel response: %w", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if accepted, err := isAcceptedMixpanelResponse(body); err != nil {
		return err
	} else if !accepted {
		return fmt.Errorf("mixpanel track request rejected: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

func isAcceptedMixpanelResponse(body []byte) (bool, error) {
	trimmed := strings.TrimSpace(string(body))
	switch trimmed {
	case "1":
		return true, nil
	case "0":
		return false, nil
	}

	var decoded mixpanelResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return false, fmt.Errorf("decode mixpanel response: %w", err)
	}
	if decoded.Status == "" {
		return false, nil
	}
	return strings.EqualFold(decoded.Status, "ok"), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if s := strings.TrimSpace(value); s != "" {
			return s
		}
	}
	return ""
}
