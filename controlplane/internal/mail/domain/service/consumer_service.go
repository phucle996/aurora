package mailSvcInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type ConsumerService interface {
	CreateConsumer(ctx context.Context, tenantID string, name string, sourceType mailEntity.SourceType, sourceConfigRef string, parallelism int) (*mailEntity.Consumer, error)
	GetConsumer(ctx context.Context, tenantID, id string) (*mailEntity.Consumer, error)
	ListConsumers(ctx context.Context, tenantID string, filterSource string, filterStatus string) ([]*mailEntity.Consumer, error)
	UpdateConsumer(ctx context.Context, tenantID, id string, name string, parallelism int) (*mailEntity.Consumer, error)
	DeleteConsumer(ctx context.Context, tenantID, id string) error
	UpdateStatus(ctx context.Context, tenantID, id string, status mailEntity.ConsumerStatus) error
	TestConnection(ctx context.Context, tenantID, id string) (bool, error)
}
