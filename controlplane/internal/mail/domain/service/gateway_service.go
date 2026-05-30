package mailSvcInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type GatewayService interface {
	CreateGateway(ctx context.Context, tenantID string, name string, routePolicy string) (*mailEntity.Gateway, error)
	GetGateway(ctx context.Context, tenantID, id string) (*mailEntity.Gateway, error)
	ListGateways(ctx context.Context, tenantID string) ([]*mailEntity.Gateway, error)
	UpdateGateway(ctx context.Context, tenantID, id string, name string, routePolicy string, isActive bool) (*mailEntity.Gateway, error)
	DeleteGateway(ctx context.Context, tenantID, id string) error
	RouteMail(ctx context.Context, tenantID string, gatewayID string, metadata map[string]interface{}) (string, error) // Returns endpoint ID
}
