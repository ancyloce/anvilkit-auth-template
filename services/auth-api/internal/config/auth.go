package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"anvilkit-auth-template/modules/common-go/pkg/analytics"
)

const (
	defaultAccessTTLMin     = 15
	defaultRefreshTTLHours  = 168
	defaultVerificationTTL  = 15
	defaultPasswordMinLen   = 8
	defaultBcryptCost       = 12
	defaultLoginFailLimit   = 5
	defaultLoginFailWindowM = 10
	defaultPublicBaseURL    = "http://localhost:8080"
)

type AuthConfig struct {
	JWTIssuer       string
	JWTAudience     string
	JWTSecret       string
	PublicBaseURL   string
	Analytics       analytics.Config
	VerificationTTL time.Duration
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	PasswordMinLen  int
	BcryptCost      int
	LoginFailLimit  int
	LoginFailWindow time.Duration
}

func LoadAuthConfigFromEnv() (AuthConfig, error) {
	issuer := strings.TrimSpace(os.Getenv("JWT_ISSUER"))
	if issuer == "" {
		return AuthConfig{}, fmt.Errorf("JWT_ISSUER is required")
	}
	audience := strings.TrimSpace(os.Getenv("JWT_AUDIENCE"))
	if audience == "" {
		return AuthConfig{}, fmt.Errorf("JWT_AUDIENCE is required")
	}
	secret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if secret == "" {
		return AuthConfig{}, fmt.Errorf("JWT_SECRET is required")
	}

	accessTTLMin, err := getPositiveIntFromEnv("ACCESS_TTL_MIN", defaultAccessTTLMin)
	if err != nil {
		return AuthConfig{}, err
	}
	refreshTTLHours, err := getPositiveIntFromEnv("REFRESH_TTL_HOURS", defaultRefreshTTLHours)
	if err != nil {
		return AuthConfig{}, err
	}
	verificationTTLMin, err := getPositiveIntFromEnv("VERIFICATION_TTL_MIN", defaultVerificationTTL)
	if err != nil {
		return AuthConfig{}, err
	}
	passwordMinLen, err := getPositiveIntFromEnv("PASSWORD_MIN_LEN", defaultPasswordMinLen)
	if err != nil {
		return AuthConfig{}, err
	}
	bcryptCost, err := getPositiveIntFromEnv("BCRYPT_COST", defaultBcryptCost)
	if err != nil {
		return AuthConfig{}, err
	}
	if bcryptCost < 4 || bcryptCost > 31 {
		return AuthConfig{}, fmt.Errorf("BCRYPT_COST must be between 4 and 31")
	}
	loginFailLimit, err := getPositiveIntFromEnv("LOGIN_FAIL_LIMIT", defaultLoginFailLimit)
	if err != nil {
		return AuthConfig{}, err
	}
	loginFailWindowMin, err := getPositiveIntFromEnv("LOGIN_FAIL_WINDOW_MIN", defaultLoginFailWindowM)
	if err != nil {
		return AuthConfig{}, err
	}
	publicBaseURL, err := getPublicBaseURLFromEnv("AUTH_PUBLIC_BASE_URL", defaultPublicBaseURL)
	if err != nil {
		return AuthConfig{}, err
	}
	analyticsCfg, err := analytics.LoadConfigFromEnv()
	if err != nil {
		return AuthConfig{}, err
	}

	return AuthConfig{
		JWTIssuer:       issuer,
		JWTAudience:     audience,
		JWTSecret:       secret,
		PublicBaseURL:   publicBaseURL,
		Analytics:       analyticsCfg,
		VerificationTTL: time.Duration(verificationTTLMin) * time.Minute,
		AccessTTL:       time.Duration(accessTTLMin) * time.Minute,
		RefreshTTL:      time.Duration(refreshTTLHours) * time.Hour,
		PasswordMinLen:  passwordMinLen,
		BcryptCost:      bcryptCost,
		LoginFailLimit:  loginFailLimit,
		LoginFailWindow: time.Duration(loginFailWindowMin) * time.Minute,
	}, nil
}

func getPositiveIntFromEnv(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid integer", key)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than 0", key)
	}
	return value, nil
}

func getPublicBaseURLFromEnv(key, def string) (string, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		raw = def
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("%s must be an absolute URL with host", key)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%s scheme must be http or https", key)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}
