package storageRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type walletAdmissionRepo struct {
	db     *pgxpool.Pool
	schema string
}

func NewWalletAdmissionRepo(db *pgxpool.Pool, cfg *config.Config) storageRepoInterface.WalletAdmissionRepo {
	return &walletAdmissionRepo{db: db, schema: cfg.SchemaSQL.Storage}
}

func (r *walletAdmissionRepo) RequireOwnerAdmission(ctx context.Context, ownerID, ownerType string) error {
	var mode string
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT admission_mode
		FROM %s.wallet_admission_projection
		WHERE owner_id=$1 AND owner_type=$2
		  AND effective_at <= NOW()
		  AND (valid_until IS NULL OR valid_until > NOW())`, r.schema), ownerID, ownerType).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return storageTaxonomy.ErrWalletAdmissionDenied
	}
	if err != nil {
		return fmt.Errorf("storage admission projection read failed: %w", err)
	}
	if mode != "ALLOW" {
		return storageTaxonomy.ErrWalletAdmissionDenied
	}
	return nil
}
