package iam

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"controlplane/internal/http/middleware"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apperr"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"
	"errors"
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

	if err := m.AdminAPIKeyService.Bootstrap(ctx); err != nil {
		if errors.Is(err, iamTaxonomy.ErrPreconditionFailed) || errors.Is(err, iamTaxonomy.ErrLockAlreadyHeld) {
			// Bootstrap đã hoàn thành ở lần chạy trước hoặc replica khác đang chạy bootstrap
			// → bỏ qua an toàn, không chặn startup.
		} else {
			return err
		}
	}
	if m.rotationCancel == nil {
		// System worker phải sống theo app lifecycle (không theo bootstrap timeout context).
		// Context này chỉ bị cancel khi Module.Stop() được gọi lúc shutdown app.
		workerCtx, cancel := context.WithCancel(context.Background())
		m.rotationCancel = cancel
		go m.runAdminRotationScheduler(workerCtx)
	}
	if m.finalizeCancel == nil {
		// Session-finalize worker cũng chạy xuyên suốt runtime và chỉ dừng khi shutdown.
		workerCtx, cancel := context.WithCancel(context.Background())
		m.finalizeCancel = cancel
		go m.runAdminSessionFinalizeScheduler(workerCtx)
	}
	if m.deviceCapCancel == nil {
		workerCtx, cancel := context.WithCancel(context.Background())
		m.deviceCapCancel = cancel
		go m.runDeviceCapReconciler(workerCtx)
	}
	return nil
}

// Stop dừng các background worker do IAM module quản lý.
//
// Hiện tại bao gồm:
// - admin key rotation scheduler caller.
func (m *IAMModule) Stop() {
	if m == nil {
		return
	}
	if m.rotationCancel != nil {
		m.rotationCancel()
		m.rotationCancel = nil
	}
	if m.finalizeCancel != nil {
		m.finalizeCancel()
		m.finalizeCancel = nil
	}
	if m.deviceCapCancel != nil {
		m.deviceCapCancel()
		m.deviceCapCancel = nil
	}
}

func (m *IAMModule) runAdminSessionFinalizeScheduler(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m == nil || m.AdminAPIKeyService == nil || m.cfg == nil {
				continue
			}
			_ = m.AdminAPIKeyService.FinalizeInactiveSessions(ctx, time.Now().UTC().Add(-m.cfg.Security.AdminSessionTTL), 200)
		}
	}
}

// runAdminRotationScheduler là caller/orchestrator cho scheduled rotation trigger.
//
// Policy runtime V1:
// - Tick base 30s + jitter nhỏ để giảm thundering herd giữa replicas.
// - Mỗi tick gọi TryProcessAdminKeyRotationTrigger(ctx) ở service.
// - lock contention được service xử lý theo no-op policy.
// - lỗi thật retry theo backoff 5s -> 15s -> 30s.
//
// Logging:
// - dùng system logger,
// - log theo run_id/attempt/reason/result để phục vụ vận hành.
func (m *IAMModule) runAdminRotationScheduler(ctx context.Context) {
	const op = "iam.rotation.scheduler"
	baseTick := 30 * time.Second
	backoffSchedule := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	runID := time.Now().UTC().Format("20060102T150405.000000000")

	logger.SysInfoFields(op, "admin rotation scheduler started", logger.Fields{"run_id": runID, "tick": baseTick.String()})
	defer logger.SysInfoFields(op, "admin rotation scheduler stopped", logger.Fields{"run_id": runID})

	attempt := 0
	for {
		jitter := time.Duration(rng.Intn(4000)) * time.Millisecond
		wait := baseTick + jitter
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		err := m.AdminAPIKeyService.TryProcessAdminKeyRotationTrigger(ctx)
		if err == nil {
			attempt = 0
			logger.SysDebugFields(op, "rotation scheduler tick completed", logger.Fields{"run_id": runID, "result": "success_or_noop", "reason": "none"})
			continue
		}
		if errors.Is(err, iamTaxonomy.ErrPreconditionFailed) {
			logger.SysInfoFields(op, "rotation scheduler lock contention", logger.Fields{"run_id": runID, "result": "noop", "reason": "lock_busy"})
			continue
		}

		if attempt < len(backoffSchedule)-1 {
			attempt++
		}
		retry := backoffSchedule[attempt]
		reason := "rotate_fail"
		// Phân loại reason từ outcome trong AppError để log vận hành chính xác
		if appErr, ok := apperr.As(err); ok && appErr.Outcome == iamMetrics.OutcomeFailureUnknown {
			reason = "rotate_delivery_fail"
		}
		logger.SysWarnFields(op, "rotation scheduler tick failed", err, logger.Fields{"run_id": runID, "attempt": attempt + 1, "reason": reason, "result": "retry", "retry_in": retry.String()})
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
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
