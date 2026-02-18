module auth-platform-template/services/admin-api

go 1.22

require (
	auth-platform-template/modules/common-go v0.0.0
	github.com/casbin/casbin/v2 v2.98.0
	github.com/gin-gonic/gin v1.10.0
	github.com/jackc/pgx/v5 v5.7.2
)

replace auth-platform-template/modules/common-go => ../../modules/common-go
