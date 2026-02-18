package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAuthConfigFromEnvMissingRequired(t *testing.T) {
	t.Setenv("JWT_ISSUER", "")
	t.Setenv("JWT_AUDIENCE", "")
	t.Setenv("JWT_SECRET", "")

	_, err := LoadAuthConfigFromEnv()
	if err == nil {
		t.Fatal("LoadAuthConfigFromEnv() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "JWT_ISSUER") {
		t.Fatalf("LoadAuthConfigFromEnv() error = %q, want mention JWT_ISSUER", err)
	}
}

func TestLoadAuthConfigFromEnvInvalidNumber(t *testing.T) {
	setRequiredAuthEnv(t)
	t.Setenv("ACCESS_TTL_MIN", "abc")

	_, err := LoadAuthConfigFromEnv()
	if err == nil {
		t.Fatal("LoadAuthConfigFromEnv() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "ACCESS_TTL_MIN") {
		t.Fatalf("LoadAuthConfigFromEnv() error = %q, want mention ACCESS_TTL_MIN", err)
	}
}

func TestLoadAuthConfigFromEnvInvalidTTLValue(t *testing.T) {
	setRequiredAuthEnv(t)
	t.Setenv("REFRESH_TTL_HOURS", "0")

	_, err := LoadAuthConfigFromEnv()
	if err == nil {
		t.Fatal("LoadAuthConfigFromEnv() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "REFRESH_TTL_HOURS") {
		t.Fatalf("LoadAuthConfigFromEnv() error = %q, want mention REFRESH_TTL_HOURS", err)
	}
}

func TestLoadAuthConfigFromEnvSuccess(t *testing.T) {
	setRequiredAuthEnv(t)
	t.Setenv("ACCESS_TTL_MIN", "20")
	t.Setenv("REFRESH_TTL_HOURS", "240")
	t.Setenv("PASSWORD_MIN_LEN", "10")
	t.Setenv("BCRYPT_COST", "13")
	t.Setenv("LOGIN_FAIL_LIMIT", "7")
	t.Setenv("LOGIN_FAIL_WINDOW_MIN", "30")

	cfg, err := LoadAuthConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadAuthConfigFromEnv() error = %v", err)
	}
	if cfg.AccessTTL != 20*time.Minute {
		t.Fatalf("AccessTTL = %v, want %v", cfg.AccessTTL, 20*time.Minute)
	}
	if cfg.RefreshTTL != 240*time.Hour {
		t.Fatalf("RefreshTTL = %v, want %v", cfg.RefreshTTL, 240*time.Hour)
	}
	if cfg.PasswordMinLen != 10 {
		t.Fatalf("PasswordMinLen = %d, want 10", cfg.PasswordMinLen)
	}
	if cfg.BcryptCost != 13 {
		t.Fatalf("BcryptCost = %d, want 13", cfg.BcryptCost)
	}
	if cfg.LoginFailLimit != 7 {
		t.Fatalf("LoginFailLimit = %d, want 7", cfg.LoginFailLimit)
	}
	if cfg.LoginFailWindow != 30*time.Minute {
		t.Fatalf("LoginFailWindow = %v, want %v", cfg.LoginFailWindow, 30*time.Minute)
	}
}

func setRequiredAuthEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_ISSUER", "anvilkit-auth")
	t.Setenv("JWT_AUDIENCE", "anvilkit-clients")
	t.Setenv("JWT_SECRET", "test-secret-value")
}
