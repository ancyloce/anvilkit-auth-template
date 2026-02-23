package handler

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/casbin/casbin/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/apperr"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/errcode"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/resp"
	"anvilkit-auth-template/services/admin-api/internal/store"
)

var manageRoles = []string{"owner", "admin"}
var allowedMemberRoles = []string{"owner", "admin", "member"}

type Handler struct {
	Store    *store.Store
	Enforcer *casbin.Enforcer
}

type listMembersResp struct {
	Members []memberItem `json:"members"`
}

type memberItem struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type addMemberReq struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type updateMemberReq struct {
	Role string `json:"role"`
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

func (h *Handler) ListMembers(c *gin.Context) error {
	tid := c.Param("tenantId")
	if err := h.requireTenantManager(c, tid); err != nil {
		return err
	}

	members, err := h.Store.ListMembers(c, tid)
	if err != nil {
		return err
	}

	items := make([]memberItem, 0, len(members))
	for _, m := range members {
		items = append(items, memberItem{UserID: m.UserID, Email: m.Email, Role: m.Role, CreatedAt: m.CreatedAt})
	}
	resp.OK(c, listMembersResp{Members: items})
	return nil
}

func (h *Handler) AddMember(c *gin.Context) error {
	tid := c.Param("tenantId")
	if err := h.requireTenantManager(c, tid); err != nil {
		return err
	}

	var req addMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err).WithData(map[string]any{"reason": "invalid_argument"})
	}
	if err := validateUserID(req.UserID); err != nil {
		return err
	}
	if err := validateRole(req.Role); err != nil {
		return err
	}

	exists, err := h.Store.UserExists(c, req.UserID)
	if err != nil {
		return err
	}
	if !exists {
		return apperr.NotFound(errors.New("user_not_found")).WithData(map[string]any{"reason": "user_not_found"})
	}

	if err = h.Store.AddMember(c, tid, req.UserID, req.Role); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" {
				return apperr.Conflict(err).WithData(map[string]any{"reason": "member_exists"})
			}
			if pgErr.Code == "23503" {
				// Foreign key violation: referenced user no longer exists.
				return apperr.NotFound(errors.New("user_not_found")).WithData(map[string]any{"reason": "user_not_found"})
			}
		}
		return err
	}
	resp.OK(c, map[string]any{"ok": true})
	return nil
}

func (h *Handler) UpdateMemberRole(c *gin.Context) error {
	tid := c.Param("tenantId")
	targetUID := c.Param("uid")
	if err := h.requireTenantManager(c, tid); err != nil {
		return err
	}
	if err := validateUserID(targetUID); err != nil {
		return err
	}

	var req updateMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err).WithData(map[string]any{"reason": "invalid_argument"})
	}
	if err := validateRole(req.Role); err != nil {
		return err
	}

	updated, err := h.Store.UpdateMemberRole(c, tid, targetUID, req.Role)
	if err != nil {
		return err
	}
	if !updated {
		return apperr.NotFound(errors.New("member_not_found")).WithData(map[string]any{"reason": "member_not_found"})
	}
	resp.OK(c, map[string]any{"ok": true})
	return nil
}

func (h *Handler) RemoveMember(c *gin.Context) error {
	tid := c.Param("tenantId")
	targetUID := c.Param("uid")
	if err := h.requireTenantManager(c, tid); err != nil {
		return err
	}
	if err := validateUserID(targetUID); err != nil {
		return err
	}

	removed, err := h.Store.RemoveMember(c, tid, targetUID)
	if err != nil {
		return err
	}
	if !removed {
		return apperr.NotFound(errors.New("member_not_found")).WithData(map[string]any{"reason": "member_not_found"})
	}
	resp.OK(c, map[string]any{"ok": true})
	return nil
}

func (h *Handler) requireTenantManager(c *gin.Context, tid string) error {
	uidAny, _ := c.Get("uid")
	uid, _ := uidAny.(string)
	role, exists, err := h.Store.TenantUserRole(c, tid, uid)
	if err != nil {
		return err
	}
	if !exists || !slices.Contains(manageRoles, role) {
		return apperr.Forbidden(errors.New("insufficient_role")).WithData(map[string]any{"reason": "insufficient_role", "code": errcode.Forbidden})
	}
	return nil
}

func validateRole(role string) error {
	if !slices.Contains(allowedMemberRoles, role) {
		return apperr.BadRequest(fmt.Errorf("invalid role: %s", role)).WithData(map[string]any{"reason": "invalid_argument"})
	}
	return nil
}

func validateUserID(id string) error {
	if strings.TrimSpace(id) == "" {
		return apperr.BadRequest(errors.New("missing_user_id")).WithData(map[string]any{"reason": "invalid_argument"})
	}
	if _, err := uuid.Parse(id); err != nil {
		return apperr.BadRequest(err).WithData(map[string]any{"reason": "invalid_argument"})
	}
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
