package main

import (
	"context"
	"log"
	"path/filepath"

	"github.com/casbin/casbin/v2"
	"github.com/gin-gonic/gin"

	"anvilkit-auth-template/modules/common-go/pkg/cfg"
	"anvilkit-auth-template/modules/common-go/pkg/db/pgsql"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/admin-api/internal/handler"
	"anvilkit-auth-template/services/admin-api/internal/store"
)

func main() {
	ctx := context.Background()
	db, err := pgsql.New(ctx, cfg.GetString("DB_DSN", "postgres://postgres:postgres@localhost:5432/auth?sslmode=disable"))
	if err != nil {
		log.Fatal(err)
	}

	rbacDir := cfg.GetString("RBAC_DIR", "internal/rbac")
	e, err := casbin.NewEnforcer(filepath.Join(rbacDir, "model.conf"), filepath.Join(rbacDir, "policy.csv"))
	if err != nil {
		log.Fatal(err)
	}

	st := &store.Store{DB: db}
	h := &handler.Handler{Store: st, Enforcer: e}
	secret := cfg.GetString("JWT_SECRET", "dev-secret-change-me")
	issuer := cfg.GetString("JWT_ISSUER", "anvilkit-auth")
	audience := cfg.GetString("JWT_AUDIENCE", "anvilkit-clients")

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(ginmid.RequestID())
	r.Use(ginmid.Logger())
	r.Use(ginmid.CORS(cfg.GetList("CORS_ALLOW_ORIGINS"), cfg.GetBool("CORS_ALLOW_CREDENTIALS", false)))
	r.Use(ginmid.ErrorHandler())

	r.NoRoute(handler.NotFound)
	r.GET("/healthz", ginmid.Wrap(h.Healthz))

	admin := r.Group("/api/v1/admin", ginmid.AuthN(secret, issuer, audience), handler.MustTenantMatch(st))
	admin.GET("/tenants/:tenantId/me/roles", ginmid.Wrap(h.MeRoles))
	admin.POST("/tenants/:tenantId/users/:userId/roles/:role", ginmid.Wrap(h.AssignRole))
	admin.GET("/tenants/:tenantId/members", ginmid.Wrap(h.ListMembers))
	admin.POST("/tenants/:tenantId/members", ginmid.Wrap(h.AddMember))
	admin.PATCH("/tenants/:tenantId/members/:uid", ginmid.Wrap(h.UpdateMemberRole))
	admin.DELETE("/tenants/:tenantId/members/:uid", ginmid.Wrap(h.RemoveMember))

	if err := r.Run(":8081"); err != nil {
		log.Fatal(err)
	}
}
