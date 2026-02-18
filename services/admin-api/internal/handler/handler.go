package handler

import (
	"fmt"
	"net/http"

	"github.com/casbin/casbin/v2"
	"github.com/gin-gonic/gin"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/apperr"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/resp"
	"anvilkit-auth-template/services/admin-api/internal/store"
)

type Handler struct {
	Store    *store.Store
	Enforcer *casbin.Enforcer
}

func (h *Handler) Healthz(c *gin.Context) error {
	resp.OK(c, map[string]any{"status": "ok"})
	return nil
}

func (h *Handler) MeRoles(c *gin.Context) error {
	uid, _ := c.Get("uid")
	tid := c.Param("tenantId")
	roles, err := h.Store.RolesForUser(c, tid, uid.(string))
	if err != nil {
		return err
	}
	if err := h.enforce(c, roles, tid); err != nil {
		return err
	}
	resp.OK(c, map[string]any{"roles": roles})
	return nil
}

func (h *Handler) AssignRole(c *gin.Context) error {
	uid, _ := c.Get("uid")
	tid := c.Param("tenantId")
	roles, err := h.Store.RolesForUser(c, tid, uid.(string))
	if err != nil {
		return err
	}
	if err = h.enforce(c, roles, tid); err != nil {
		return err
	}
	if err = h.Store.AssignRole(c, tid, c.Param("userId"), c.Param("role")); err != nil {
		return err
	}
	resp.OK(c, map[string]any{"assigned": true})
	return nil
}

func (h *Handler) enforce(c *gin.Context, roles []string, tid string) error {
	dom := fmt.Sprintf("tenant:%s", tid)
	obj := c.FullPath()
	act := c.Request.Method
	for _, role := range roles {
		ok, err := h.Enforcer.Enforce(role, dom, obj, act)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return apperr.Forbidden(nil).WithData(map[string]any{"reason": "rbac_denied", "code": errcode.Forbidden})
}

func NotFound(c *gin.Context) {
	resp.Fail(c, http.StatusNotFound, errcode.NotFound, "not_found", map[string]any{"reason": "route_not_found"})
}
