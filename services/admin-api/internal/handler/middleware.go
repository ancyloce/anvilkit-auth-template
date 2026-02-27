package handler

import (
	"errors"
	"fmt"

	"github.com/casbin/casbin/v2"
	"github.com/gin-gonic/gin"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/apperr"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/services/admin-api/internal/rbac"
	"anvilkit-auth-template/services/admin-api/internal/store"
)

func AdminRBAC(st *store.Store, enforcer *casbin.Enforcer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if enforcer == nil {
			_ = c.Error(apperr.Forbidden(errors.New("casbin_denied")).WithData(map[string]any{"reason": "casbin_denied", "code": errcode.Forbidden}))
			c.Abort()
			return
		}

		uidAny, uidExists := c.Get("uid")
		uid, _ := uidAny.(string)
		if !uidExists || uid == "" {
			_ = c.Error(apperr.Unauthorized(errors.New("missing_uid")).WithData(map[string]any{"reason": "missing_uid"}))
			c.Abort()
			return
		}

		pathTid := tenantIDFromPath(c)
		if pathTid == "" {
			_ = c.Error(apperr.Forbidden(errors.New("tenant_mismatch")).WithData(map[string]any{"reason": "tenant_mismatch", "code": errcode.Forbidden}))
			c.Abort()
			return
		}

		tokenTidAny, _ := c.Get("tid")
		tokenTid, _ := tokenTidAny.(string)
		if tokenTid != "" && tokenTid != pathTid {
			_ = c.Error(apperr.Forbidden(errors.New("tenant_mismatch")).WithData(map[string]any{"reason": "tenant_mismatch", "code": errcode.Forbidden}))
			c.Abort()
			return
		}

		tenantRole, exists, err := st.TenantUserRole(c, pathTid, uid)
		if err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}
		if !exists {
			_ = c.Error(apperr.Forbidden(errors.New("not_in_tenant")).WithData(map[string]any{"reason": "not_in_tenant", "code": errcode.Forbidden}))
			c.Abort()
			return
		}

		casbinRole, err := rbac.MapTenantRoleToCasbin(tenantRole)
		if err != nil {
			_ = c.Error(apperr.Forbidden(errors.New("insufficient_role")).WithData(map[string]any{"reason": "insufficient_role", "code": errcode.Forbidden}))
			c.Abort()
			return
		}
		obj := c.FullPath()
		act := c.Request.Method
		dom := fmt.Sprintf("tenant:%s", pathTid)
		ok, err := enforcer.Enforce(casbinRole, dom, obj, act)
		if err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}
		if !ok {
			_ = c.Error(apperr.Forbidden(errors.New("casbin_denied")).WithData(map[string]any{"reason": "casbin_denied", "code": errcode.Forbidden}))
			c.Abort()
			return
		}

		c.Next()
	}
}

func tenantIDFromPath(c *gin.Context) string {
	if tid := c.Param("tid"); tid != "" {
		return tid
	}
	return c.Param("tenantId")
}
