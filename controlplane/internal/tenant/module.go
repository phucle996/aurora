package tenant

import (
	"errors"

	"controlplane/internal/config"
	tenantRepoImpl "controlplane/internal/tenant/repository"
	tenantSvcImpl "controlplane/internal/tenant/service"
	tenantHandler "controlplane/internal/tenant/transport/http/handler"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Module struct {
	TenantHandler *tenantHandler.Handler
}

func NewModule(cfg *config.Config, db *pgxpool.Pool) (*Module, error) {
	if cfg == nil {
		return nil, errors.New("tenant module: config is required")
	}
	repo := tenantRepoImpl.NewRepository(cfg, db)
	svc := tenantSvcImpl.NewService(repo)
	h := tenantHandler.NewHandler(svc)
	return &Module{TenantHandler: h}, nil
}
