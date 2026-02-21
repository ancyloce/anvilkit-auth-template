package ginmid

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	ajwt "anvilkit-auth-template/modules/common-go/pkg/auth/jwt"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/apperr"
)

func AuthN(secret, issuer, audience string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(raw, "Bearer ") {
			_ = c.Error(apperr.Unauthorized(errors.New("missing_bearer")).WithData(map[string]any{"reason": "missing_bearer"}))
			c.Abort()
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
		claims, err := ajwt.Parse(secret, issuer, audience, token)
		if err != nil || claims.Typ != "access" {
			_ = c.Error(apperr.Unauthorized(errors.New("invalid_access_token")).WithData(map[string]any{"reason": "invalid_access_token"}))
			c.Abort()
			return
		}

		uid := claims.Subject
		if uid == "" {
			uid = claims.UID
		}
		if uid == "" {
			_ = c.Error(apperr.Unauthorized(errors.New("missing_sub")).WithData(map[string]any{"reason": "missing_sub"}))
			c.Abort()
			return
		}

		c.Set("uid", uid)
		c.Set("tid", claims.TID)
		c.Next()
	}
}
