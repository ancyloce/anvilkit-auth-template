package ginmid

import (
	"context"
	"errors"
	"fmt"
	"time"

	"auth-platform-template/modules/common-go/pkg/httpx/apperr"
	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
)

func RateLimit(rdb *goredis.Client, keyPrefix string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if rdb == nil {
			c.Next()
			return
		}
		ctx := context.Background()
		key := fmt.Sprintf("%s:%s", keyPrefix, c.ClientIP())
		n, err := rdb.Incr(ctx, key).Result()
		if err != nil {
			_ = c.Error(apperr.RateLimited(err))
			c.Abort()
			return
		}
		if n == 1 {
			_ = rdb.Expire(ctx, key, window).Err()
		}
		if int(n) > limit {
			_ = c.Error(apperr.RateLimited(errors.New("too_many_requests")))
			c.Abort()
			return
		}
		c.Next()
	}
}
