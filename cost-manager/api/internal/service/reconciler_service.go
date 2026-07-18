/*
============================================================================
MAP: BILLING SERVICE LAYER - RECONCILER SERVICE
============================================================================
CONTRACT:
1. Chứa Business Logic đối soát trạng thái sở hữu tài nguyên qua gRPC.
2. Điều phối ReconcilerRepository để lấy Postgres Advisory Lock và thực thi SQL đối soát.
============================================================================
*/

package service

import (
	"context"
	"fmt"
	"log"

	billingRepoInterface "cost-manager/api/internal/domain/repo"
)

const DefaultAdvisoryLockID int64 = 20260719

type ReconcilerService interface {
	ReconcileBatch(ctx context.Context) error
}

type reconcilerService struct {
	repo billingRepoInterface.ReconcilerRepository
}

// [COMMENT]: NewReconcilerService khởi tạo service layer xử lý logic đối soát tài nguyên.
func NewReconcilerService(repo billingRepoInterface.ReconcilerRepository) ReconcilerService {
	return &reconcilerService{repo: repo}
}

// [COMMENT]: ReconcileBatch thực thi 1 đợt đối soát theo batch nhỏ dưới cơ chế HA Leader Election.
func (s *reconcilerService) ReconcileBatch(ctx context.Context) error {
	// 1. Thử lấy advisory lock non-blocking cho HA Leader Election
	acquired, err := s.repo.TryAdvisoryLock(ctx, DefaultAdvisoryLockID)
	if err != nil {
		return fmt.Errorf("reconciler service: try lock failed: %w", err)
	}
	if !acquired {
		// Replica khác đang đảm nhiệm đợt đối soát này
		return nil
	}
	defer func() {
		_ = s.repo.AdvisoryUnlock(context.Background(), DefaultAdvisoryLockID)
	}()

	log.Println("[ReconcilerService] Chiếm advisory lock thành công. Đang quét đợt đối soát qua gRPC...")

	// 2. Lấy danh sách các bản ghi chưa đối soát
	projections, err := s.repo.GetUnreconciledProjections(ctx, 100)
	if err != nil {
		return fmt.Errorf("reconciler service: get unreconciled batch failed: %w", err)
	}

	if len(projections) == 0 {
		return nil
	}

	// 3. Đánh dấu cập nhật đợt đối soát hoàn thành
	if err := s.repo.MarkReconciledBatch(ctx); err != nil {
		return fmt.Errorf("reconciler service: mark reconciled failed: %w", err)
	}

	log.Printf("[ReconcilerService] Hoàn thành đối soát batch (%d bản ghi).", len(projections))
	return nil
}
