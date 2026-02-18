package ginmid

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func CORS(allowOrigins []string, allowCredentials bool) gin.HandlerFunc {
	allowed := map[string]struct{}{}
	for _, o := range allowOrigins {
		allowed[strings.TrimSpace(o)] = struct{}{}
	}

	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if _, ok := allowed[origin]; ok {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		if allowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-Id")
		c.Header("Access-Control-Expose-Headers", "X-Request-Id")

		if c.Request.Method == "OPTIONS" {
			c.Status(204)
			c.Abort()
			return
		}
		c.Next()
	}
}
