package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/apperr"
)

func MustTenantMatch() gin.HandlerFunc {
	return func(c *gin.Context) {
		tid, _ := c.Get("tid")
		pathTid := c.Param("tenantId")
		if tid == nil || tid.(string) != pathTid {
			_ = c.Error(apperr.Forbidden(errors.New("tenant_mismatch")))
			c.Abort()
			return
		}
		c.Next()
	}
}
