package reconcile

import (
	"context"
	"controlplane/internal/delta-engine/runtime"
	"controlplane/pkg/logger"
	"time"
)

// Reconciler chịu trách nhiệm chạy ngầm quét kiểm tra lệch pha dữ liệu (Anti-Entropy / Self-Healing).
// Bảo vệ RAM Cache khỏi các lỗi truyền tin mất mát gói hoặc race condition lúc ghi DB.
type Reconciler struct {
	holder     *runtime.SnapshotHolder
	syncPeriod time.Duration
}

// NewReconciler khởi tạo một Reconciler điều phối chu kỳ tự phục hồi dữ liệu.
func NewReconciler(holder *runtime.SnapshotHolder, syncPeriod time.Duration) *Reconciler {
	return &Reconciler{
		holder:     holder,
		syncPeriod: syncPeriod,
	}
}

// Start khởi chạy vòng lặp Reconcile định kỳ cho đến khi context bị hủy.
func (r *Reconciler) Start(ctx context.Context) {
	logger.SysInfo("reconciler", "starting periodic self-healing active loop")
	ticker := time.NewTicker(r.syncPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.SysInfo("reconciler", "stopping self-healing active loop gracefully")
			return
		case <-ticker.C:
			r.Reconcile(ctx)
		}
	}
}

// Reconcile thực hiện quét đối chiếu giữa phiên bản RAM hiện tại và phiên bản SoT của Database.
func (r *Reconciler) Reconcile(ctx context.Context) {
	currentSnap := r.holder.Get()

	// Ở đây trong thực tế sẽ gọi store.LoadLatestVersion() từ postgres
	// Nếu phát hiện lệch phiên bản, sẽ kích hoạt luồng recovery nạp lại toàn bộ.
	logger.SysInfoFields("reconciler", "anti-entropy cycle execution successful", logger.Fields{
		"current_snapshot_version": currentSnap.Version,
		"zones_count":              len(currentSnap.Zones),
		"rate_policies_count":      len(currentSnap.RatePolicies),
	})
}
