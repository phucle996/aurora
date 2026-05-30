package mailSvcImpl

import (
	"context"
	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
)

type gatewayServiceImpl struct {
	cfg         *config.Config
	gatewayRepo mailRepoInterface.GatewayRepository
}

func NewGatewayService(cfg *config.Config, gatewayRepo mailRepoInterface.GatewayRepository) mailSvcInterface.GatewayService {
	return &gatewayServiceImpl{
		cfg:         cfg,
		gatewayRepo: gatewayRepo,
	}
}

func (s *gatewayServiceImpl) CreateGateway(ctx context.Context, tenantID string, name string, routePolicy string) (*mailEntity.Gateway, error) {
	return nil, nil
}

func (s *gatewayServiceImpl) GetGateway(ctx context.Context, tenantID, id string) (*mailEntity.Gateway, error) {
	return nil, nil
}

func (s *gatewayServiceImpl) ListGateways(ctx context.Context, tenantID string) ([]*mailEntity.Gateway, error) {
	return nil, nil
}

func (s *gatewayServiceImpl) UpdateGateway(ctx context.Context, tenantID, id string, name string, routePolicy string, isActive bool) (*mailEntity.Gateway, error) {
	return nil, nil
}

func (s *gatewayServiceImpl) DeleteGateway(ctx context.Context, tenantID, id string) error {
	return nil
}

func (s *gatewayServiceImpl) RouteMail(ctx context.Context, tenantID string, gatewayID string, metadata map[string]interface{}) (string, error) {
	return "", nil
}
