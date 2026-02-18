package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAccessTTLMin     = 15
	defaultRefreshTTLHours  = 168
	defaultPasswordMinLen   = 8
	defaultBcryptCost       = 12
	defaultLoginFailLimit   = 5
	defaultLoginFailWindowM = 10
)

type AuthConfig struct {
	JWTIssuer       string
	JWTAudience     string
	JWTSecret       string
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

	return AuthConfig{
		JWTIssuer:       issuer,
		JWTAudience:     audience,
		JWTSecret:       secret,
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
