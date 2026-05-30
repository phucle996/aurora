package mailRepoImpl

import (
	"context"
	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"

	"github.com/jackc/pgx/v5/pgxpool"
)

type consumerRepoPostgres struct {
	db     *pgxpool.Pool
	schema string
}

func NewConsumerRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.ConsumerRepository {
	return &consumerRepoPostgres{
		db:     db,
		schema: cfg.SchemaSQL.Mail,
	}
}

func (r *consumerRepoPostgres) Create(ctx context.Context, c *mailEntity.Consumer) error {
	// Skeleton implementation
	return nil
}

func (r *consumerRepoPostgres) GetByID(ctx context.Context, tenantID, id string) (*mailEntity.Consumer, error) {
	// Skeleton implementation
	return nil, nil
}

func (r *consumerRepoPostgres) List(ctx context.Context, tenantID string, filterSource string, filterStatus string) ([]*mailEntity.Consumer, error) {
	// Skeleton implementation
	return nil, nil
}

func (r *consumerRepoPostgres) Update(ctx context.Context, c *mailEntity.Consumer) error {
	// Skeleton implementation
	return nil
}

func (r *consumerRepoPostgres) Delete(ctx context.Context, tenantID, id string) error {
	// Skeleton implementation
	return nil
}
