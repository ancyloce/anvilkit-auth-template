package dto

// Bootstrap

type BootstrapRequest struct {
	TenantName    string `json:"tenant_name" binding:"required"`
	OwnerEmail    string `json:"owner_email" binding:"required,email"`
	OwnerPassword string `json:"owner_password" binding:"required"`
}

type BootstrapResponse struct {
	Tenant    TenantSummary `json:"tenant"`
	OwnerUser UserSummary   `json:"owner_user"`
}

// Register

type RegisterRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type RegisterResponse struct {
	User UserSummary `json:"user"`
}

// Login

type LoginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	AccessToken      string      `json:"access_token"`
	ExpiresIn        int         `json:"expires_in"`
	RefreshToken     string      `json:"refresh_token"`
	RefreshExpiresIn int         `json:"refresh_expires_in"`
	User             UserSummary `json:"user"`
}

// Refresh

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type RefreshResponse struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

// Logout

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type LogoutResponse struct {
	OK bool `json:"ok"`
}

// Logout all

type LogoutAllResponse struct {
	OK           bool  `json:"ok"`
	RevokedCount int64 `json:"revoked_count"`
}

// Shared payloads

type UserSummary struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type TenantSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Switch tenant

type SwitchTenantRequest struct {
	TenantID string `json:"tenant_id" binding:"required"`
}

type SwitchTenantResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}
