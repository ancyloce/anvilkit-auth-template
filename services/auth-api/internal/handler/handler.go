package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"

	ajwt "anvilkit-auth-template/modules/common-go/pkg/auth/jwt"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/apperr"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/resp"
	"anvilkit-auth-template/modules/common-go/pkg/util"
	"anvilkit-auth-template/services/auth-api/internal/auth/token"
	"anvilkit-auth-template/services/auth-api/internal/store"
)

type Handler struct {
	Store           *store.Store
	JWTSecret       string
	JWTIssuer       string
	JWTAudience     string
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	PasswordMinLen  int
	BcryptCost      int
	Redis           *goredis.Client
	LoginFailLimit  int
	LoginFailWindow time.Duration
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
	res, err := h.Store.Bootstrap(c, req.Email, req.Password, req.Tenant, h.BcryptCost)
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
	var req struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if _, err := mail.ParseAddress(email); err != nil {
		return apperr.BadRequest(fmt.Errorf("invalid_email"))
	}
	if len(req.Password) < h.PasswordMinLen {
		return apperr.BadRequest(fmt.Errorf("password_too_short"))
	}
	user, err := h.Store.Register(c, email, req.Password, h.BcryptCost)
	if err != nil {
		return err
	}
	c.JSON(http.StatusCreated, resp.Envelope{
		RequestID: c.GetString("request_id"),
		Code:      0,
		Message:   "ok",
		Data: map[string]any{
			"user": map[string]any{"id": user.ID, "email": user.Email},
		},
	})
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
	at, err := ajwt.Sign(h.JWTSecret, h.JWTIssuer, h.JWTAudience, uid, tid, "access", h.AccessTTL)
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
	at, err := ajwt.Sign(h.JWTSecret, h.JWTIssuer, h.JWTAudience, uid, tid, "access", h.AccessTTL)
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

func (h *Handler) LoginSession(c *gin.Context) error {
	var req struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if _, err := mail.ParseAddress(email); err != nil {
		return apperr.BadRequest(fmt.Errorf("invalid_email"))
	}

	ip := c.ClientIP()
	failKey := fmt.Sprintf("login_fail:%s:%s", ip, email)

	// Check rate limit before touching DB.
	if h.Redis != nil && h.LoginFailLimit > 0 {
		n, err := h.Redis.Get(c.Request.Context(), failKey).Int()
		if err == nil && n >= h.LoginFailLimit {
			return apperr.RateLimited(errors.New("too_many_requests"))
		}
	}

	incFail := func() {
		if h.Redis == nil {
			return
		}
		ctx := c.Request.Context()
		// Fixed-window: set the TTL only when the key is first created.
		n, err := h.Redis.Incr(ctx, failKey).Result()
		if err == nil && n == 1 {
			_ = h.Redis.Expire(ctx, failKey, h.LoginFailWindow).Err()
		}
	}

	u, err := h.Store.VerifyEmailPassword(c.Request.Context(), email, req.Password)
	if err != nil {
		incFail()
		return apperr.Unauthorized(errors.New("invalid_credentials"))
	}

	// Clear fail counter on success.
	if h.Redis != nil {
		h.Redis.Del(c.Request.Context(), failKey)
	}

	rt, err := token.GenRefreshToken(32)
	if err != nil {
		return err
	}
	rtHash := token.HashRefreshToken(rt)
	rtExp := time.Now().Add(h.RefreshTTL)

	sessionID := uuid.NewString()
	if err = h.Store.CreateRefreshSession(
		c.Request.Context(), sessionID, u.ID, rtHash,
		c.GetHeader("User-Agent"), ip, rtExp,
	); err != nil {
		return err
	}

	// tid is intentionally empty: /v1/auth/login is tenant-agnostic.
	at, err := ajwt.Sign(h.JWTSecret, h.JWTIssuer, h.JWTAudience, u.ID, "", "access", h.AccessTTL)
	if err != nil {
		return err
	}

	resp.OK(c, map[string]any{
		"access_token":       at,
		"expires_in":         int(h.AccessTTL.Seconds()),
		"refresh_token":      rt,
		"refresh_expires_in": int(h.RefreshTTL.Seconds()),
		"user":               map[string]any{"id": u.ID, "email": u.Email},
	})
	return nil
}

func NotFound(c *gin.Context) {
	resp.Fail(c, http.StatusNotFound, 1004, "not_found", map[string]any{"reason": "route_not_found"})
}
