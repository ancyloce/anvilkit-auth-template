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
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"

	ajwt "anvilkit-auth-template/modules/common-go/pkg/auth/jwt"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/apperr"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/resp"
	"anvilkit-auth-template/modules/common-go/pkg/util"
	"anvilkit-auth-template/services/auth-api/internal/auth/crypto"
	"anvilkit-auth-template/services/auth-api/internal/store"
)

const userStatusActive int16 = 1

type Handler struct {
	Store           *store.Store
	Redis           *goredis.Client
	JWTIssuer       string
	JWTAudience     string
	JWTSecret       string
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	PasswordMinLen  int
	BcryptCost      int
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
	at, rt, err := h.issueTokens(c, res.UserID, res.TenantID, c.GetHeader("User-Agent"), c.ClientIP())
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
	if strings.TrimSpace(req.Password) == "" {
		return apperr.BadRequest(fmt.Errorf("password_required"))
	}

	ip := c.ClientIP()
	key := fmt.Sprintf("login_fail:%s:%s", ip, email)
	if blocked, err := h.isLoginRateLimited(c, key); err != nil {
		return err
	} else if blocked {
		return apperr.RateLimited(errors.New("login_rate_limited"))
	}

	user, err := h.Store.GetLoginUserByEmail(c, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.increaseLoginFailCount(c, key)
			return apperr.Unauthorized(errors.New("invalid_credentials"))
		}
		return err
	}
	if user.Status != userStatusActive || crypto.VerifyPassword(user.PasswordHash, req.Password) != nil {
		h.increaseLoginFailCount(c, key)
		return apperr.Unauthorized(errors.New("invalid_credentials"))
	}

	at, rt, err := h.issueTokens(c, user.ID, "", c.GetHeader("User-Agent"), ip)
	if err != nil {
		return err
	}
	if h.Redis != nil {
		_ = h.Redis.Del(c, key).Err()
	}

	resp.OK(c, map[string]any{
		"access_token":       at,
		"expires_in":         int(h.AccessTTL.Seconds()),
		"refresh_token":      rt,
		"refresh_expires_in": int(h.RefreshTTL.Seconds()),
		"user":               map[string]any{"id": user.ID, "email": user.Email},
	})
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
	uid, _, err := h.Store.RotateRefreshToken(c, req.RefreshToken, newRT, time.Now().Add(h.RefreshTTL))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.Unauthorized(err)
		}
		return err
	}
	at, err := ajwt.Sign(h.JWTSecret, h.JWTIssuer, h.JWTAudience, uid, "", "access", h.AccessTTL)
	if err != nil {
		return err
	}
	resp.OK(c, map[string]any{"access_token": at, "refresh_token": newRT, "user_id": uid})
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

func (h *Handler) issueTokens(ctx context.Context, uid, tid, userAgent, ip string) (string, string, error) {
	at, err := ajwt.Sign(h.JWTSecret, h.JWTIssuer, h.JWTAudience, uid, tid, "access", h.AccessTTL)
	if err != nil {
		return "", "", err
	}
	rt, err := util.RandomToken(32)
	if err != nil {
		return "", "", err
	}
	if err = h.Store.SaveRefreshSession(ctx, rt, uid, time.Now().Add(h.RefreshTTL), userAgent, ip); err != nil {
		return "", "", err
	}
	return at, rt, nil
}

func (h *Handler) isLoginRateLimited(ctx context.Context, key string) (bool, error) {
	if h.Redis == nil {
		return false, nil
	}
	count, err := h.Redis.Get(ctx, key).Int()
	if err != nil && !errors.Is(err, goredis.Nil) {
		return false, err
	}
	return count >= h.LoginFailLimit, nil
}

func (h *Handler) increaseLoginFailCount(ctx context.Context, key string) {
	if h.Redis == nil {
		return
	}
	count, err := h.Redis.Incr(ctx, key).Result()
	if err != nil {
		return
	}
	if count == 1 {
		_ = h.Redis.Expire(ctx, key, h.LoginFailWindow).Err()
	}
}

func NotFound(c *gin.Context) {
	resp.Fail(c, http.StatusNotFound, 1004, "not_found", map[string]any{"reason": "route_not_found"})
}
