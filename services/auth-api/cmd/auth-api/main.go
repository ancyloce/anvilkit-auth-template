package main

import (
	"context"
	"log"
	"time"

	"github.com/gin-gonic/gin"

	"anvilkit-auth-template/modules/common-go/pkg/cache/redis"
	"anvilkit-auth-template/modules/common-go/pkg/cfg"
	"anvilkit-auth-template/modules/common-go/pkg/db/pgsql"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/ginmid"
	"anvilkit-auth-template/services/auth-api/internal/config"
	"anvilkit-auth-template/services/auth-api/internal/handler"
	"anvilkit-auth-template/services/auth-api/internal/store"
)

func main() {
	ctx := context.Background()
	db, err := pgsql.New(ctx, cfg.GetString("DB_DSN", "postgres://postgres:postgres@localhost:5432/auth?sslmode=disable"))
	if err != nil {
		log.Fatal(err)
	}
	rdb, err := redis.New(ctx, cfg.GetString("REDIS_ADDR", "localhost:6379"))
	if err != nil {
		log.Fatal(err)
	}
	authCfg, err := config.LoadAuthConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	h := &handler.Handler{
		Store:           &store.Store{DB: db},
		JWTSecret:       authCfg.JWTSecret,
		JWTIssuer:       authCfg.JWTIssuer,
		JWTAudience:     authCfg.JWTAudience,
		AccessTTL:       authCfg.AccessTTL,
		RefreshTTL:      authCfg.RefreshTTL,
		PasswordMinLen:  authCfg.PasswordMinLen,
		BcryptCost:      authCfg.BcryptCost,
		Redis:           rdb,
		LoginFailLimit:  authCfg.LoginFailLimit,
		LoginFailWindow: authCfg.LoginFailWindow,
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(ginmid.RequestID())
	r.Use(ginmid.Logger())
	r.Use(ginmid.CORS(cfg.GetList("CORS_ALLOW_ORIGINS"), cfg.GetBool("CORS_ALLOW_CREDENTIALS", false)))
	r.Use(ginmid.ErrorHandler())

	r.NoRoute(handler.NotFound)
	r.GET("/healthz", ginmid.Wrap(h.Healthz))

	api := r.Group("/api/v1/auth")
	api.POST("/bootstrap", ginmid.RateLimit(rdb, "rl:bootstrap", 10, time.Minute), ginmid.Wrap(h.Bootstrap))
	api.POST("/register", ginmid.RateLimit(rdb, "rl:register", 20, time.Minute), ginmid.Wrap(h.Register))
	api.POST("/login", ginmid.RateLimit(rdb, "rl:login", 30, time.Minute), ginmid.Wrap(h.Login))
	api.POST("/refresh", ginmid.Wrap(h.Refresh))
	api.POST("/logout", ginmid.AuthN(h.JWTSecret), ginmid.Wrap(h.Logout))

	v1 := r.Group("/v1/auth")
	v1.POST("/register", ginmid.RateLimit(rdb, "rl:register", 20, time.Minute), ginmid.Wrap(h.Register))
	v1.POST("/login", ginmid.Wrap(h.LoginSession))

	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
