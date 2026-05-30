package mailRepoImpl

import (
	"context"
	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"

	"github.com/jackc/pgx/v5/pgxpool"
)

type templateRepoPostgres struct {
	db     *pgxpool.Pool
	schema string
}

func NewTemplateRepository(db *pgxpool.Pool, cfg *config.Config) mailRepoInterface.TemplateRepository {
	return &templateRepoPostgres{
		db:     db,
		schema: cfg.SchemaSQL.Mail,
	}
}

func (r *templateRepoPostgres) Create(ctx context.Context, t *mailEntity.Template) error {
	// Skeleton implementation
	return nil
}

func (r *templateRepoPostgres) GetByID(ctx context.Context, tenantID, id string) (*mailEntity.Template, error) {
	// Skeleton implementation
	return nil, nil
}

func (r *templateRepoPostgres) List(ctx context.Context, tenantID string) ([]*mailEntity.Template, error) {
	// Skeleton implementation
	return nil, nil
}

func (r *templateRepoPostgres) Update(ctx context.Context, t *mailEntity.Template) error {
	// Skeleton implementation
	return nil
}

func (r *templateRepoPostgres) Delete(ctx context.Context, tenantID, id string) error {
	// Skeleton implementation
	return nil
}
