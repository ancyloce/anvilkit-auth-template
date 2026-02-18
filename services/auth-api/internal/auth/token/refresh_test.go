package token

import (
	"regexp"
	"testing"
)

func TestGenRefreshToken(t *testing.T) {
	token, err := GenRefreshToken(32)
	if err != nil {
		t.Fatalf("GenRefreshToken() error = %v", err)
	}
	if token == "" {
		t.Fatal("GenRefreshToken() token is empty")
	}
	if len(token) < 40 {
		t.Fatalf("GenRefreshToken() token length = %d, want >= 40", len(token))
	}
	base64URLPattern := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	if !base64URLPattern.MatchString(token) {
		t.Fatalf("GenRefreshToken() token = %q, want base64url characters only", token)
	}
}

func TestHashRefreshTokenStable(t *testing.T) {
	const input = "example-refresh-token"
	h1 := HashRefreshToken(input)
	h2 := HashRefreshToken(input)
	if h1 != h2 {
		t.Fatalf("HashRefreshToken() mismatch: %q != %q", h1, h2)
	}
}
