package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

const defaultRefreshTokenBytes = 32

func GenRefreshToken(nBytes int) (string, error) {
	if nBytes <= 0 {
		nBytes = defaultRefreshTokenBytes
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func ValidateRefreshTokenFormat(token string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("refresh token is required")
	}
	if strings.ContainsAny(token, "+/=") {
		return fmt.Errorf("refresh token must be base64url without padding")
	}
	if _, err := base64.RawURLEncoding.DecodeString(token); err != nil {
		return fmt.Errorf("refresh token must be valid base64url")
	}
	return nil
}
