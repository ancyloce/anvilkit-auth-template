package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/apperr"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/services/admin-api/internal/store"
)

func MustTenantMatch(st *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenTidAny, _ := c.Get("tid")
		pathTid := c.Param("tenantId")
		uidAny, _ := c.Get("uid")
		uid, _ := uidAny.(string)

		tokenTid, _ := tokenTidAny.(string)
		if tokenTid != "" && tokenTid != pathTid {
			_ = c.Error(apperr.Forbidden(errors.New("tenant_mismatch")).WithData(map[string]any{"reason": "tenant_mismatch", "code": errcode.Forbidden}))
			c.Abort()
			return
		}

		if tokenTid == "" {
			_, exists, err := st.TenantUserRole(c, pathTid, uid)
			if err != nil {
				_ = c.Error(err)
				c.Abort()
				return
			}
			if !exists {
				_ = c.Error(apperr.Forbidden(errors.New("tenant_mismatch")).WithData(map[string]any{"reason": "tenant_mismatch", "code": errcode.Forbidden}))
				c.Abort()
				return
			}
		}

		c.Next()
	}
}
