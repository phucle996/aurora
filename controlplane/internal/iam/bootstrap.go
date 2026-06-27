package iam

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"controlplane/internal/http/middleware"
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
	// Khởi động warm-up toàn bộ System Roles vào L1 Cache lúc boot
	if err := m.WarmUpSystemRoles(ctx); err != nil {
		logger.SysError("iam.rbac.warmup.failed", err.Error())
		return fmt.Errorf("iam rbac warm up: %w", err)
	}

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

			processed, err := m.deviceSvcImpl.ReconcileDeviceCap(ctx, 100)
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

// WarmUpSystemRoles nạp sẵn toàn bộ System Roles vào L1 Cache lúc ứng dụng boot.
func (m *IAMModule) WarmUpSystemRoles(ctx context.Context) error {
	const op = "iam.rbac.warmup"
	logger.SysInfoFields(op, "warming up system roles", logger.Fields{})

	if m.L1Registry == nil || m.RbacRepository == nil {
		return fmt.Errorf("iam rbac warmup: dependencies not fully initialized")
	}

	entries, err := m.RbacRepository.ListSystemRoleEntries(ctx)
	if err != nil {
		return fmt.Errorf("warm up system roles: %w", err)
	}

	for _, entry := range entries {
		if entry == nil || entry.Role == nil {
			continue
		}
		roleCode := entry.Role.Code
		// Gọi GetOrLoad để nạp và kích hoạt cache L1 cho role hệ thống
		_, err := m.L1Registry.GetOrLoad(ctx, "rbac_role", roleCode)
		if err != nil {
			logger.SysError(op, fmt.Sprintf("failed to warm up system role %q: %v", roleCode, err))
			return err
		}
		// Đăng ký vào middleware.GlobalSystemRoleRegistry để phân định L1/L2
		middleware.RegisterSystemRole(roleCode)
	}
	logger.SysInfoFields(op, fmt.Sprintf("successfully warmed up %d system roles", len(entries)), logger.Fields{})
	return nil
}
