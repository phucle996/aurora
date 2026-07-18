/*
============================================================================
MAP: BILLING TRANSPORT LAYER - RPC RECONCILER WORKER
============================================================================
CONTRACT:
1. Đặt tại transport/rpc theo đúng phân lớp kiến trúc.
2. Quản lý vòng lặp ticker bất đồng bộ và gọi sang ReconcilerService để thực thi đợt đối soát.
============================================================================
*/

package rpc

import (
	"context"
	"log"
	"time"

	"cost-manager/api/internal/service"
)

// [COMMENT]: StorageOwnershipReconcilerWorker điều phối vòng lặp đối soát định kỳ theo chu kỳ interval.
type StorageOwnershipReconcilerWorker struct {
	reconcilerSvc service.ReconcilerService
	interval      time.Duration
	stopChan      chan struct{}
}

// [COMMENT]: NewStorageOwnershipReconcilerWorker khởi tạo RPC worker cho đối soát sở hữu tài nguyên.
func NewStorageOwnershipReconcilerWorker(reconcilerSvc service.ReconcilerService, interval time.Duration) *StorageOwnershipReconcilerWorker {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &StorageOwnershipReconcilerWorker{
		reconcilerSvc: reconcilerSvc,
		interval:      interval,
		stopChan:      make(chan struct{}),
	}
}

// [COMMENT]: Start khởi chạy vòng lặp ticker đối soát trong goroutine bất đồng bộ.
func (w *StorageOwnershipReconcilerWorker) Start(ctx context.Context) {
	log.Printf("[RPC Reconciler] Bắt đầu gRPC reconciliation loop với chu kỳ %v...", w.interval)

	go func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-w.stopChan:
				log.Println("[RPC Reconciler] Đã dừng reconciler loop.")
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := w.reconcilerSvc.ReconcileBatch(ctx); err != nil {
					log.Printf("[RPC Reconciler] Lỗi trong đợt reconcile batch: %v", err)
				}
			}
		}
	}()
}

// [COMMENT]: Stop phát tín hiệu dừng vòng lặp đối soát an toàn.
func (w *StorageOwnershipReconcilerWorker) Stop() {
	close(w.stopChan)
}
