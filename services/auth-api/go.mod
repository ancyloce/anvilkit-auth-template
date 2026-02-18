module auth-platform-template/services/auth-api

go 1.22

require (
	auth-platform-template/modules/common-go v0.0.0
	github.com/gin-gonic/gin v1.10.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.2
	github.com/redis/go-redis/v9 v9.7.1
	golang.org/x/crypto v0.32.0
)

replace auth-platform-template/modules/common-go => ../../modules/common-go
