package rbac

import (
	"context"
	"fmt"

	"github.com/casbin/casbin/v2"
)

func NewEnforcer(ctx context.Context, dbDSN, modelPath string) (*casbin.Enforcer, error) {
	adapter, err := NewPostgresAdapter(ctx, dbDSN)
	if err != nil {
		return nil, fmt.Errorf("init casbin postgres adapter: %w", err)
	}
	enforcer, err := casbin.NewEnforcer(modelPath, adapter)
	if err != nil {
		return nil, fmt.Errorf("create casbin enforcer: %w", err)
	}
	if err = enforcer.LoadPolicy(); err != nil {
		return nil, fmt.Errorf("load casbin policy: %w", err)
	}
	return enforcer, nil
}
