package iam

import (
	"context"
	"math/rand"
	"time"

	iamMetrics "controlplane/internal/iam/metrics"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"
)

// Bootstrap khởi tạo runtime side-effects của IAM module.
//
// Trách nhiệm:
//  1. chạy admin bootstrap idempotent ở service layer,
//  2. khởi động scheduler caller cho admin key rotation trigger.
//
// Lưu ý:
// - Business logic rotate nằm ở service/repo.
// - Bootstrap chỉ orchestration, không chứa persistence logic.
func (m *IAMModule) Bootstrap(ctx context.Context) error {
	// [COMMENT]: System roles warm up is deprecated since permissions are computed statically and loaded lazily from binary bytea mapping tables.
	if m.deviceCapCancel == nil {
		workerCtx, cancel := context.WithCancel(context.Background())
		m.deviceCapCancel = cancel
		go m.runDeviceCapReconciler(workerCtx)
	}
	return nil
}

// Stop dừng các background worker do IAM module quản lý.
func (m *IAMModule) Stop() {
	if m == nil {
		return
	}
	if m.deviceCapCancel != nil {
		m.deviceCapCancel()
		m.deviceCapCancel = nil
	}
	// [COMMENT]: Dừng các tác vụ nền bất đồng bộ của Auth Service và đợi hoàn thành (Graceful Shutdown)
	if m.AuthService != nil {
		m.AuthService.Stop()
	}
}

// runDeviceCapReconciler vá drift do lock skip ở login flow (BR-009).
// Tick mỗi 60s, tối đa 100 user/batch.
func (m *IAMModule) runDeviceCapReconciler(ctx context.Context) {
	const op = "iam.device_cap.reconciler"
	ctx = constant.WithOperation(ctx, "device_cap_reconcile")
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	// initial jitter 0-30s để các replica không cùng tick ngay sau restart
	initialDelay := time.Duration(rng.Intn(30000)) * time.Millisecond
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	logger.SysInfoFields(op, "device cap reconciler started", logger.Fields{"tick": "60s", "batch": 100, "initial_delay": initialDelay.String()})
	defer logger.SysInfoFields(op, "device cap reconciler stopped", logger.Fields{})
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:

			// [COMMENT]: ReconcileDeviceCap định kỳ dọn dẹp các thiết bị vượt ngưỡng thông qua DeviceSelfService
			processed, err := m.deviceSelfSvcImpl.ReconcileDeviceCap(ctx, 100)
			if err != nil {
				iamMetrics.ServiceCall(ctx, iamMetrics.OutcomeFailureUnknown)
				logger.SysWarnFields(op, "reconcile failed", err, logger.Fields{})
				continue
			}
			if processed > 0 {
				logger.SysInfoFields(op, "reconcile fixed drift", logger.Fields{"processed_users": processed})
			}
			iamMetrics.ServiceCall(ctx, iamMetrics.OutcomeSuccess)
		}
	}
}
