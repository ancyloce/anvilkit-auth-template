package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	ajwt "anvilkit-auth-template/modules/common-go/pkg/auth/jwt"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/apperr"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/resp"
	"anvilkit-auth-template/modules/common-go/pkg/util"
	"anvilkit-auth-template/services/auth-api/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type Handler struct {
	Store      *store.Store
	JWTSecret  string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

type authReq struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	TenantID string `json:"tenant_id"`
	Tenant   string `json:"tenant_name"`
}

func (h *Handler) Healthz(c *gin.Context) error {
	resp.OK(c, map[string]any{"status": "ok"})
	return nil
}

func (h *Handler) Bootstrap(c *gin.Context) error {
	var req authReq
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}
	if strings.TrimSpace(req.Tenant) == "" {
		return apperr.BadRequest(errors.New("tenant_name_required"))
	}
	res, err := h.Store.Bootstrap(c, req.Email, req.Password, req.Tenant)
	if err != nil {
		if err.Error() == "invalid_password" {
			return apperr.Unauthorized(err)
		}
		return err
	}
	at, rt, err := h.issueTokens(c, res.UserID, res.TenantID)
	if err != nil {
		return err
	}
	resp.OK(c, map[string]any{"user_id": res.UserID, "tenant_id": res.TenantID, "access_token": at, "refresh_token": rt})
	return nil
}

func (h *Handler) Register(c *gin.Context) error {
	var req authReq
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}
	uid, err := h.Store.Register(c, req.Email, req.Password)
	if err != nil {
		return err
	}
	resp.OK(c, map[string]any{"user_id": uid})
	return nil
}

func (h *Handler) Login(c *gin.Context) error {
	var req authReq
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}
	if strings.TrimSpace(req.TenantID) == "" {
		return apperr.BadRequest(errors.New("tenant_id_required"))
	}
	uid, err := h.Store.Login(c, req.Email, req.Password, req.TenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || err.Error() == "invalid_password" {
			return apperr.Unauthorized(err)
		}
		return err
	}
	at, rt, err := h.issueTokens(c, uid, req.TenantID)
	if err != nil {
		return err
	}
	resp.OK(c, map[string]any{"user_id": uid, "tenant_id": req.TenantID, "access_token": at, "refresh_token": rt})
	return nil
}

func (h *Handler) Refresh(c *gin.Context) error {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}
	newRT, err := util.RandomToken(32)
	if err != nil {
		return err
	}
	uid, tid, err := h.Store.RotateRefreshToken(c, req.RefreshToken, newRT, time.Now().Add(h.RefreshTTL))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.Unauthorized(err)
		}
		return err
	}
	at, err := ajwt.Sign(h.JWTSecret, uid, tid, "access", h.AccessTTL)
	if err != nil {
		return err
	}
	resp.OK(c, map[string]any{"access_token": at, "refresh_token": newRT, "tenant_id": tid, "user_id": uid})
	return nil
}

func (h *Handler) Logout(c *gin.Context) error {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}
	if err := h.Store.RevokeRefreshToken(c, req.RefreshToken); err != nil {
		return err
	}
	resp.OK(c, map[string]any{"revoked": true})
	return nil
}

func (h *Handler) issueTokens(ctx context.Context, uid, tid string) (string, string, error) {
	at, err := ajwt.Sign(h.JWTSecret, uid, tid, "access", h.AccessTTL)
	if err != nil {
		return "", "", err
	}
	rt, err := util.RandomToken(32)
	if err != nil {
		return "", "", err
	}
	if err = h.Store.SaveRefreshToken(ctx, rt, uid, tid, time.Now().Add(h.RefreshTTL)); err != nil {
		return "", "", err
	}
	return at, rt, nil
}

func NotFound(c *gin.Context) {
	resp.Fail(c, http.StatusNotFound, 1004, "not_found", map[string]any{"reason": "route_not_found"})
}
