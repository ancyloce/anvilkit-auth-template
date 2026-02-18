package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
)

func New(ctx context.Context, addr string) (*goredis.Client, error) {
	c := goredis.NewClient(&goredis.Options{Addr: addr})
	if err := c.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return c, nil
}
