package ginmid

import (
	"errors"
	"strings"

	ajwt "auth-platform-template/modules/common-go/pkg/auth/jwt"
	"auth-platform-template/modules/common-go/pkg/httpx/apperr"
	"github.com/gin-gonic/gin"
)

func AuthN(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(raw, "Bearer ") {
			_ = c.Error(apperr.Unauthorized(errors.New("missing_bearer")))
			c.Abort()
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
		claims, err := ajwt.Parse(secret, token)
		if err != nil || claims.Typ != "access" {
			_ = c.Error(apperr.Unauthorized(errors.New("invalid_access_token")))
			c.Abort()
			return
		}
		c.Set("uid", claims.UID)
		c.Set("tid", claims.TID)
		c.Next()
	}
}
