package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"anvilkit-auth-template/modules/common-go/pkg/analytics"
	"anvilkit-auth-template/services/auth-api/internal/store"
	"anvilkit-auth-template/services/auth-api/internal/testutil"
)

type capturedEvent struct {
	Name       string
	UserID     string
	Email      string
	Timestamp  time.Time
	Properties map[string]any
}

type fakeAnalytics struct {
	mu     sync.Mutex
	events []capturedEvent
	err    error
}

func (f *fakeAnalytics) Track(_ context.Context, event analytics.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	props := make(map[string]any, len(event.Properties))
	for k, v := range event.Properties {
		props[k] = v
	}
	f.events = append(f.events, capturedEvent{
		Name:       event.Name,
		UserID:     event.UserID,
		Email:      event.Email,
		Timestamp:  event.Timestamp,
		Properties: props,
	})
	return f.err
}

func newTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testutil.MustTestDB(t)
}

func newTestRedis(t *testing.T) *goredis.Client {
	t.Helper()
	return testutil.MustTestRedis(t)
}

func mustJWTSecret(t *testing.T) string {
	t.Helper()
	return testutil.MustJWTSecret(t)
}

func performJSONRequest(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func performAuthedJSONRequest(t *testing.T, r *gin.Engine, method, path, accessToken string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeResponse(t *testing.T, res *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(res.Body.Bytes(), out); err != nil {
		t.Fatalf("json unmarshal: %v body=%s", err, res.Body.String())
	}
}

func newTestAuthHandler(t *testing.T, db *pgxpool.Pool, rdb *goredis.Client) *Handler {
	t.Helper()
	return &Handler{
		Store:           &store.Store{DB: db},
		Redis:           rdb,
		Analytics:       analytics.NoopClient{},
		JWTIssuer:       "anvilkit-auth",
		JWTAudience:     "anvilkit-clients",
		JWTSecret:       mustJWTSecret(t),
		PublicBaseURL:   "http://auth.example.com",
		VerificationTTL: 15 * time.Minute,
		AccessTTL:       15 * time.Minute,
		RefreshTTL:      168 * time.Hour,
		PasswordMinLen:  8,
		BcryptCost:      4,
		LoginFailLimit:  5,
		LoginFailWindow: 10 * time.Minute,
	}
}
