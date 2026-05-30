package mailRepoImpl

import (
	"context"
	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"

	"github.com/jackc/pgx/v5/pgxpool"
)

type gatewayRepoPostgres struct {
	db     *pgxpool.Pool
	schema string
}

func NewGatewayRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.GatewayRepository {
	return &gatewayRepoPostgres{
		db:     db,
		schema: cfg.SchemaSQL.Mail,
	}
}

func (r *gatewayRepoPostgres) Create(ctx context.Context, g *mailEntity.Gateway) error {
	// Skeleton implementation
	return nil
}

func (r *gatewayRepoPostgres) GetByID(ctx context.Context, tenantID, id string) (*mailEntity.Gateway, error) {
	// Skeleton implementation
	return nil, nil
}

func (r *gatewayRepoPostgres) List(ctx context.Context, tenantID string) ([]*mailEntity.Gateway, error) {
	// Skeleton implementation
	return nil, nil
}

func (r *gatewayRepoPostgres) Update(ctx context.Context, g *mailEntity.Gateway) error {
	// Skeleton implementation
	return nil
}

func (r *gatewayRepoPostgres) Delete(ctx context.Context, tenantID, id string) error {
	// Skeleton implementation
	return nil
}
