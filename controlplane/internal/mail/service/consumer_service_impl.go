package mailSvcImpl

import (
	"context"
	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
)

type consumerServiceImpl struct {
	cfg          *config.Config
	consumerRepo mailRepoInterface.ConsumerRepository
}

func NewConsumerService(cfg *config.Config, consumerRepo mailRepoInterface.ConsumerRepository) mailSvcInterface.ConsumerService {
	return &consumerServiceImpl{
		cfg:          cfg,
		consumerRepo: consumerRepo,
	}
}

func (s *consumerServiceImpl) CreateConsumer(ctx context.Context, tenantID string, name string, sourceType mailEntity.SourceType, sourceConfigRef string, parallelism int) (*mailEntity.Consumer, error) {
	return nil, nil
}

func (s *consumerServiceImpl) GetConsumer(ctx context.Context, tenantID, id string) (*mailEntity.Consumer, error) {
	return nil, nil
}

func (s *consumerServiceImpl) ListConsumers(ctx context.Context, tenantID string, filterSource string, filterStatus string) ([]*mailEntity.Consumer, error) {
	return nil, nil
}

func (s *consumerServiceImpl) UpdateConsumer(ctx context.Context, tenantID, id string, name string, parallelism int) (*mailEntity.Consumer, error) {
	return nil, nil
}

func (s *consumerServiceImpl) DeleteConsumer(ctx context.Context, tenantID, id string) error {
	return nil
}

func (s *consumerServiceImpl) UpdateStatus(ctx context.Context, tenantID, id string, status mailEntity.ConsumerStatus) error {
	return nil
}

func (s *consumerServiceImpl) TestConnection(ctx context.Context, tenantID, id string) (bool, error) {
	return false, nil
}
