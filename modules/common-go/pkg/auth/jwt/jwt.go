package jwt

import (
	"errors"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UID string `json:"uid"`
	TID string `json:"tid"`
	Typ string `json:"typ"`
	jwtv5.RegisteredClaims
}

func Sign(secret string, uid string, tid string, typ string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UID: uid,
		TID: tid,
		Typ: typ,
		RegisteredClaims: jwtv5.RegisteredClaims{
			IssuedAt:  jwtv5.NewNumericDate(now),
			ExpiresAt: jwtv5.NewNumericDate(now.Add(ttl)),
			Subject:   uid,
		},
	}
	token := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func Parse(secret, tokenStr string) (*Claims, error) {
	t, err := jwtv5.ParseWithClaims(tokenStr, &Claims{}, func(token *jwtv5.Token) (any, error) {
		if token.Method != jwtv5.SigningMethodHS256 {
			return nil, errors.New("invalid_signing_method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := t.Claims.(*Claims)
	if !ok || !t.Valid {
		return nil, errors.New("invalid_token")
	}
	return claims, nil
}
