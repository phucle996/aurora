package hypervisorRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HypervisorCommercialAdmissionRepo struct {
	db     *pgxpool.Pool
	schema string
}

func NewHypervisorCommercialAdmissionRepo(db *pgxpool.Pool, cfg *config.Config) hypervisorRepoInterface.CommercialAdmissionRepository {
	return &HypervisorCommercialAdmissionRepo{db: db, schema: cfg.SchemaSQL.Hypervisor}
}

func (r *HypervisorCommercialAdmissionRepo) RequirePersonalOwnerAdmission(ctx context.Context, ownerID uuid.UUID) error {
	var mode string
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT decision FROM %s.commercial_admission_projection
		WHERE owner_id=$1 AND owner_type='PERSONAL'
		  AND effective_at <= NOW() AND (valid_until IS NULL OR valid_until > NOW())
	`, r.schema), ownerID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && mode != "ALLOW") {
		return hypervisorTaxonomy.ErrCommercialAdmissionDenied
	}
	if err != nil {
		return fmt.Errorf("hypervisor commercial admission read: %w", err)
	}
	return nil
}
