package ginmid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	ajwt "anvilkit-auth-template/modules/common-go/pkg/auth/jwt"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/resp"
)

const (
	testJWTSecret   = "test-secret"
	testJWTIssuer   = "anvilkit-auth"
	testJWTAudience = "anvilkit-clients"
)

type testEnvelope struct {
	RequestID string          `json:"request_id"`
	Code      int             `json:"code"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
}

func TestAuthN_UnauthorizedScenarios(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		authorization string
		buildToken    func(t *testing.T) string
	}{
		{
			name: "missing Authorization",
		},
		{
			name:          "non bearer format",
			authorization: "Basic abc",
		},
		{
			name:          "invalid token",
			authorization: "Bearer not-a-jwt",
		},
		{
			name: "expired token",
			buildToken: func(t *testing.T) string {
				t.Helper()
				token, err := ajwt.Sign(testJWTSecret, testJWTIssuer, testJWTAudience, "uid-expired", "", "access", -1*time.Minute)
				if err != nil {
					t.Fatalf("sign token: %v", err)
				}
				return token
			},
		},
		{
			name: "issuer mismatch",
			buildToken: func(t *testing.T) string {
				t.Helper()
				token, err := ajwt.Sign(testJWTSecret, "another-issuer", testJWTAudience, "uid-issuer", "", "access", time.Minute)
				if err != nil {
					t.Fatalf("sign token: %v", err)
				}
				return token
			},
		},
		{
			name: "audience mismatch",
			buildToken: func(t *testing.T) string {
				t.Helper()
				token, err := ajwt.Sign(testJWTSecret, testJWTIssuer, "another-audience", "uid-audience", "", "access", time.Minute)
				if err != nil {
					t.Fatalf("sign token: %v", err)
				}
				return token
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newAuthNTestRouter(t)
			req, _ := http.NewRequest(http.MethodGet, "/protected", nil)
			authorization := tt.authorization
			if tt.buildToken != nil {
				authorization = "Bearer " + tt.buildToken(t)
			}
			if authorization != "" {
				req.Header.Set("Authorization", authorization)
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d want=%d body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
			}

			var body testEnvelope
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != errcode.Unauthorized {
				t.Fatalf("code=%d want=%d", body.Code, errcode.Unauthorized)
			}
			if body.Message != "unauthorized" {
				t.Fatalf("message=%q want=unauthorized", body.Message)
			}
		})
	}
}

func TestAuthN_SuccessSetsUIDAndTIDInContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	token, err := ajwt.Sign(testJWTSecret, testJWTIssuer, testJWTAudience, "uid-ok", "tenant-ok", "access", time.Minute)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	r := newAuthNTestRouter(t)
	req, _ := http.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			UID string `json:"uid"`
			TID string `json:"tid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 {
		t.Fatalf("code=%d want=0", body.Code)
	}
	if body.Data.UID != "uid-ok" {
		t.Fatalf("uid=%q want=uid-ok", body.Data.UID)
	}
	if body.Data.TID != "tenant-ok" {
		t.Fatalf("tid=%q want=tenant-ok", body.Data.TID)
	}
}

func TestAuthN_SuccessWithoutTIDDoesNotPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)

	token, err := ajwt.Sign(testJWTSecret, testJWTIssuer, testJWTAudience, "uid-ok", "", "access", time.Minute)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	r := newAuthNTestRouter(t)
	req, _ := http.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			UID string `json:"uid"`
			TID string `json:"tid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.TID != "" {
		t.Fatalf("tid=%q want empty", body.Data.TID)
	}
}

func newAuthNTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	r := gin.New()
	r.Use(RequestID(), ErrorHandler())
	r.GET("/protected", AuthN(testJWTSecret, testJWTIssuer, testJWTAudience), func(c *gin.Context) {
		uid, ok := c.Get("uid")
		if !ok {
			t.Fatalf("uid should exist in context")
		}
		tid := c.GetString("tid")
		resp.OK(c, map[string]any{"uid": uid, "tid": tid})
	})
	return r
}
