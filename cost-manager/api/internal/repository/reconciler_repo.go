/*
============================================================================
MAP: BILLING REPOSITORY IMPLEMENTATION - RECONCILER
============================================================================
CONTRACT:
1. Thực thi các câu lệnh PostgreSQL SQL nguyên tử phục vụ gRPC Reconciler.
2. Sử dụng pg_try_advisory_lock / pg_advisory_unlock để chống race-condition trong HA replica cluster.
============================================================================
*/

package repository

import (
	"context"
	"fmt"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type reconcilerRepository struct {
	db *pgxpool.Pool
}

// [COMMENT]: NewReconcilerRepository khởi tạo instance thực thi interface ReconcilerRepository.
func NewReconcilerRepository(db *pgxpool.Pool) billingRepoInterface.ReconcilerRepository {
	return &reconcilerRepository{db: db}
}

// [COMMENT]: TryAdvisoryLock thử chiếm PostgreSQL Advisory Lock non-blocking cho HA leader election.
func (r *reconcilerRepository) TryAdvisoryLock(ctx context.Context, lockID int64) (bool, error) {
	var acquired bool
	err := r.db.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, lockID).Scan(&acquired)
	if err != nil {
		return false, fmt.Errorf("reconciler repo: try advisory lock: %w", err)
	}
	return acquired, nil
}

// [COMMENT]: AdvisoryUnlock giải phóng PostgreSQL Advisory Lock.
func (r *reconcilerRepository) AdvisoryUnlock(ctx context.Context, lockID int64) error {
	_, err := r.db.Exec(ctx, `SELECT pg_advisory_unlock($1)`, lockID)
	if err != nil {
		return fmt.Errorf("reconciler repo: advisory unlock: %w", err)
	}
	return nil
}

// [COMMENT]: GetUnreconciledProjections quét các bản ghi sở hữu chưa được đối soát trong billing schema.
func (r *reconcilerRepository) GetUnreconciledProjections(ctx context.Context, limit int) ([]*entity.UnreconciledProjection, error) {

	rows, err := r.db.Query(ctx,
		`SELECT id, resource_id 
		 FROM billing.resource_ownership_projection
		 WHERE effective_to IS NULL
		 ORDER BY reconciled_at ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("reconciler repo: query unreconciled projections: %w", err)
	}
	defer rows.Close()

	var result []*entity.UnreconciledProjection
	for rows.Next() {
		var id, resourceID uuid.UUID
		if err := rows.Scan(&id, &resourceID); err != nil {
			continue
		}
		result = append(result, &entity.UnreconciledProjection{
			ID:         id,
			ResourceID: resourceID,
		})
	}

	return result, nil
}

// [COMMENT]: MarkReconciledBatch cập nhật mốc thời gian đối soát cho batch tài nguyên hiện tại.
func (r *reconcilerRepository) MarkReconciledBatch(ctx context.Context) error {
	_, err := r.db.Exec(ctx,
		`UPDATE billing.resource_ownership_projection 
		 SET reconciled_at = NOW() 
		 WHERE effective_to IS NULL AND reconciled_at < NOW() - INTERVAL '5 minutes'`)
	if err != nil {
		return fmt.Errorf("reconciler repo: mark reconciled batch: %w", err)
	}
	return nil
}
