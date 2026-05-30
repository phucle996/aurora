package mailSvcInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"

	"github.com/google/uuid"
)

type EndpointService interface {
	CreateEndpoint(ctx context.Context, params mailEntity.CreateEndpointParams) error
	GetEndpoint(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) (*mailEntity.Endpoint, error)
	ListEndpoints(ctx context.Context, zoneID uuid.UUID) ([]*mailEntity.Endpoint, error)
	UpdateEndpoint(ctx context.Context, params mailEntity.UpdateEndpointParams) (*mailEntity.Endpoint, error)
	DeleteEndpoint(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) error
	
	// TestConnection performs full network handshake and auth validation against a saved endpoint
	TestConnection(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) error
	
	// TestConnectionRaw performs the handshake using un-saved transient parameters (perfect for UI checks)
	TestConnectionRaw(ctx context.Context, provider mailEntity.ProviderType, plainConfig map[string]interface{}) error
}
