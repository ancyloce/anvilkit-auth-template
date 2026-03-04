package handler

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"

	ajwt "anvilkit-auth-template/modules/common-go/pkg/auth/jwt"
	commonemail "anvilkit-auth-template/modules/common-go/pkg/email"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/apperr"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/resp"
	"anvilkit-auth-template/modules/common-go/pkg/queue"
	"anvilkit-auth-template/modules/common-go/pkg/util"
	"anvilkit-auth-template/services/auth-api/internal/auth/crypto"
	"anvilkit-auth-template/services/auth-api/internal/handler/dto"
	"anvilkit-auth-template/services/auth-api/internal/store"
)

const (
	userStatusActive            int16 = 1
	emailQueueName                    = "email:send"
	verificationTTL                   = 15 * time.Minute
	verificationEmailSubject          = "Verify your email"
	verificationAcceptedMessage       = "registration accepted, please check your email for verification"
)

var otpCodePattern = regexp.MustCompile(`^\d{6}$`)

type emailSendJob struct {
	RecordID  string `json:"record_id"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	HTMLBody  string `json:"html_body"`
	TextBody  string `json:"text_body"`
	OTP       string `json:"otp"`
	MagicLink string `json:"magic_link"`
}

type Handler struct {
	Store           *store.Store
	Redis           *goredis.Client
	JWTIssuer       string
	JWTAudience     string
	JWTSecret       string
	PublicBaseURL   string
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	PasswordMinLen  int
	BcryptCost      int
	LoginFailLimit  int
	LoginFailWindow time.Duration
}

func (h *Handler) Healthz(c *gin.Context) error {
	resp.OK(c, map[string]any{"status": "ok"})
	return nil
}

func (h *Handler) Bootstrap(c *gin.Context) error {
	var req dto.BootstrapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}

	tenantName := strings.TrimSpace(req.TenantName)
	if tenantName == "" {
		return apperr.BadRequest(errors.New("tenant_name_required"))
	}
	ownerEmail := strings.TrimSpace(strings.ToLower(req.OwnerEmail))
	if ownerEmail == "" {
		return apperr.BadRequest(errors.New("owner_email_required"))
	}
	if strings.TrimSpace(req.OwnerPassword) == "" {
		return apperr.BadRequest(errors.New("owner_password_required"))
	}
	if len(req.OwnerPassword) < h.PasswordMinLen {
		return apperr.BadRequest(errors.New("password_too_short"))
	}

	res, err := h.Store.Bootstrap(c, ownerEmail, req.OwnerPassword, tenantName, h.BcryptCost)
	if err != nil {
		if errors.Is(err, store.ErrBootstrapPasswordMismatch) {
			return apperr.Unauthorized(err).WithData(map[string]any{"reason": "owner_password_mismatch"})
		}
		if errors.Is(err, store.ErrTenantNameConflict) {
			return apperr.Conflict(err).WithData(map[string]any{"reason": "tenant_name_conflict"})
		}
		return err
	}
	c.JSON(http.StatusCreated, resp.Envelope{
		RequestID: c.GetString("request_id"),
		Code:      0,
		Message:   "ok",
		Data: dto.BootstrapResponse{
			Tenant:    dto.TenantSummary{ID: res.TenantID, Name: res.TenantName},
			OwnerUser: dto.UserSummary{ID: res.UserID, Email: res.UserEmail},
		},
	})
	return nil
}

func (h *Handler) Register(c *gin.Context) error {
	var req dto.RegisterRequest
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
	otp, err := commonemail.GenerateOTP()
	if err != nil {
		return err
	}
	magicToken, err := commonemail.GenerateMagicToken()
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(verificationTTL)
	registered, err := h.Store.RegisterWithVerification(c, email, req.Password, h.BcryptCost, otp, magicToken, expiresAt)
	if err != nil {
		return err
	}

	magicLink := buildMagicLink(h.PublicBaseURL, magicToken)
	htmlBody, textBody := buildVerificationEmailBody(otp, magicLink)
	if h.Redis == nil {
		if cleanupErr := h.Store.CleanupPendingRegistration(c, registered.User.ID); cleanupErr != nil {
			return fmt.Errorf("redis unavailable; cleanup pending registration: %v", cleanupErr)
		}
		return errors.New("redis_unavailable")
	}
	q, err := queue.New(h.Redis)
	if err != nil {
		if cleanupErr := h.Store.CleanupPendingRegistration(c, registered.User.ID); cleanupErr != nil {
			return fmt.Errorf("init queue: %w; cleanup pending registration: %v", err, cleanupErr)
		}
		return err
	}
	if err := q.EnqueueContext(c, emailQueueName, emailSendJob{
		RecordID:  registered.EmailRecordID,
		To:        registered.User.Email,
		Subject:   verificationEmailSubject,
		HTMLBody:  htmlBody,
		TextBody:  textBody,
		OTP:       otp,
		MagicLink: magicLink,
	}); err != nil {
		if cleanupErr := h.Store.CleanupPendingRegistration(c, registered.User.ID); cleanupErr != nil {
			return fmt.Errorf("enqueue verification email job: %w; cleanup pending registration: %v", err, cleanupErr)
		}
		return err
	}

	c.JSON(http.StatusAccepted, resp.Envelope{
		RequestID: c.GetString("request_id"),
		Code:      0,
		Message:   verificationAcceptedMessage,
		Data: dto.RegisterResponse{
			User: dto.UserSummary{ID: registered.User.ID, Email: registered.User.Email},
		},
	})
	return nil
}

func (h *Handler) VerifyEmail(c *gin.Context) error {
	var req dto.VerifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}

	emailAddr := strings.TrimSpace(strings.ToLower(req.Email))
	if _, err := mail.ParseAddress(emailAddr); err != nil {
		return apperr.BadRequest(fmt.Errorf("invalid_email"))
	}
	otp := strings.TrimSpace(req.OTP)
	if !otpCodePattern.MatchString(otp) {
		return apperr.BadRequest(errors.New("invalid_otp")).WithData(map[string]any{"reason": "invalid_otp"})
	}

	if err := h.Store.VerifyEmailOTP(c, emailAddr, otp, time.Now()); err != nil {
		if errors.Is(err, store.ErrInvalidVerificationOTP) {
			return apperr.BadRequest(err).WithData(map[string]any{"reason": "invalid_otp"})
		}
		if errors.Is(err, store.ErrVerificationExpired) {
			return apperr.BadRequest(err).WithData(map[string]any{"reason": "expired_otp"})
		}
		return err
	}

	resp.OK(c, dto.VerifyEmailResponse{Message: "Email verified successfully"})
	return nil
}

func (h *Handler) VerifyMagicLink(c *gin.Context) error {
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		return apperr.BadRequest(errors.New("invalid_magic_link")).WithData(map[string]any{"reason": "invalid_magic_link"})
	}

	if err := h.Store.VerifyMagicLinkToken(c, token, time.Now()); err != nil {
		if errors.Is(err, store.ErrInvalidMagicLink) {
			return apperr.BadRequest(err).WithData(map[string]any{"reason": "invalid_magic_link"})
		}
		if errors.Is(err, store.ErrVerificationExpired) {
			return apperr.BadRequest(err).WithData(map[string]any{"reason": "expired_magic_link"})
		}
		return err
	}

	resp.OK(c, dto.VerifyEmailResponse{Message: "Email verified successfully"})
	return nil
}

func buildMagicLink(publicBaseURL, token string) string {
	baseURL := strings.TrimSpace(publicBaseURL)
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	u, err := url.Parse(baseURL)
	if err != nil || !u.IsAbs() || strings.TrimSpace(u.Host) == "" {
		u = &url.URL{Scheme: "http", Host: "localhost:8080"}
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/auth/verify-magic-link"
	query := url.Values{}
	query.Set("token", token)
	u.RawQuery = query.Encode()
	return u.String()
}

func buildVerificationEmailBody(otp, magicLink string) (string, string) {
	safeOTP := html.EscapeString(otp)
	safeMagicLink := html.EscapeString(magicLink)
	htmlBody := fmt.Sprintf(
		`<p>Use this verification code: <strong>%s</strong></p><p>Or verify with this magic link: <a href="%s">%s</a></p>`,
		safeOTP,
		safeMagicLink,
		safeMagicLink,
	)
	textBody := fmt.Sprintf(
		"Use this verification code: %s\nVerify with this magic link: %s",
		otp,
		magicLink,
	)
	return htmlBody, textBody
}

func (h *Handler) Login(c *gin.Context) error {
	var req dto.LoginRequest
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
	if user.Status != userStatusActive {
		return apperr.Unauthorized(errors.New("invalid_credentials"))
	}
	if crypto.VerifyPassword(user.PasswordHash, req.Password) != nil {
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

	resp.OK(c, dto.LoginResponse{
		AccessToken:      at,
		ExpiresIn:        int(h.AccessTTL.Round(time.Second).Seconds()),
		RefreshToken:     rt,
		RefreshExpiresIn: int(h.RefreshTTL.Round(time.Second).Seconds()),
		User:             dto.UserSummary{ID: user.ID, Email: user.Email},
	})
	return nil
}

func (h *Handler) Refresh(c *gin.Context) error {
	var req dto.RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}
	newRT, err := util.RandomToken(32)
	if err != nil {
		return err
	}
	uid, _, err := h.Store.RotateRefreshToken(c, req.RefreshToken, newRT, time.Now().Add(h.RefreshTTL))
	if err != nil {
		if errors.Is(err, store.ErrRefreshSessionNotFound) {
			return apperr.Unauthorized(err)
		}
		if errors.Is(err, store.ErrRefreshExpired) {
			return apperr.Unauthorized(err).WithData(map[string]any{"reason": "refresh_expired"})
		}
		if errors.Is(err, store.ErrRefreshSessionRevoked) {
			return apperr.Unauthorized(err).WithData(map[string]any{"reason": "session_revoked"})
		}
		return err
	}
	at, err := ajwt.SignAccessToken(h.JWTSecret, h.JWTIssuer, h.JWTAudience, uid, nil, h.AccessTTL)
	if err != nil {
		return err
	}
	resp.OK(c, dto.RefreshResponse{
		AccessToken:      at,
		ExpiresIn:        int(h.AccessTTL.Round(time.Second).Seconds()),
		RefreshToken:     newRT,
		RefreshExpiresIn: int(h.RefreshTTL.Round(time.Second).Seconds()),
	})
	return nil
}

func (h *Handler) Logout(c *gin.Context) error {
	var req dto.LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}
	if err := h.Store.RevokeRefreshToken(c, req.RefreshToken); err != nil {
		return err
	}
	resp.OK(c, dto.LogoutResponse{OK: true})
	return nil
}

func (h *Handler) LogoutAll(c *gin.Context) error {
	uid := strings.TrimSpace(c.GetString("uid"))
	if uid == "" {
		return apperr.Unauthorized(errors.New("invalid_access_token"))
	}
	revokedCount, err := h.Store.RevokeAllRefreshTokensByUser(c, uid)
	if err != nil {
		return err
	}
	resp.OK(c, dto.LogoutAllResponse{OK: true, RevokedCount: revokedCount})
	return nil
}

func (h *Handler) SwitchTenant(c *gin.Context) error {
	uid := strings.TrimSpace(c.GetString("uid"))
	if uid == "" {
		return apperr.Unauthorized(errors.New("invalid_access_token")).WithData(map[string]any{"reason": "invalid_access_token"})
	}

	var req dto.SwitchTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return apperr.BadRequest(err)
	}
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		return apperr.BadRequest(errors.New("tenant_id_required"))
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return apperr.BadRequest(errors.New("invalid_tenant_id"))
	}

	if err := h.Store.EnsureUserInTenant(c, uid, tenantID); err != nil {
		if errors.Is(err, store.ErrNotInTenant) {
			return apperr.Forbidden(err).WithData(map[string]any{"reason": "not_in_tenant"})
		}
		return err
	}

	at, err := ajwt.SignAccessToken(h.JWTSecret, h.JWTIssuer, h.JWTAudience, uid, &tenantID, h.AccessTTL)
	if err != nil {
		return err
	}
	resp.OK(c, dto.SwitchTenantResponse{AccessToken: at, ExpiresIn: int(h.AccessTTL.Round(time.Second).Seconds())})
	return nil
}

func (h *Handler) issueTokens(ctx context.Context, uid, tid, userAgent, ip string) (string, string, error) {
	var tenantID *string
	if strings.TrimSpace(tid) != "" {
		tenantID = &tid
	}
	at, err := ajwt.SignAccessToken(h.JWTSecret, h.JWTIssuer, h.JWTAudience, uid, tenantID, h.AccessTTL)
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
