package iam

import (
	"context"
	"math/rand"
	"time"

	iamErrorx "controlplane/internal/iam/errorx"
	iamMetrics "controlplane/internal/iam/metrics"
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
func (m *Module) Bootstrap(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if m.adminAPIKeyService == nil {
		return nil
	}

	if err := m.adminAPIKeyService.Bootstrap(ctx, "system-bootstrap"); err != nil {
		if errors.Is(err, iamErrorx.ErrAdminBootstrapNotAllowed) {
			// Bootstrap already completed in a previous run.
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
	if m.deviceCapCancel == nil && m.authSvcImpl != nil {
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
func (m *Module) Stop() {
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
	if m.rbacSync != nil {
		m.rbacSync.Stop()
	}
}

func (m *Module) runAdminSessionFinalizeScheduler(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m == nil || m.adminAPIKeyService == nil || m.cfg == nil {
				continue
			}
			_ = m.adminAPIKeyService.FinalizeInactiveSessions(ctx, time.Now().UTC().Add(-m.cfg.Security.AdminSessionTTL), 200)
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
func (m *Module) runAdminRotationScheduler(ctx context.Context) {
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

		err := m.adminAPIKeyService.TryProcessAdminKeyRotationTrigger(ctx)
		if err == nil {
			attempt = 0
			iamMetrics.ObserveAdminKeyRotationOutcome(iamMetrics.OutcomeSuccess)
			logger.SysDebugFields(op, "rotation scheduler tick completed", logger.Fields{"run_id": runID, "result": "success_or_noop", "reason": "none"})
			continue
		}
		if errors.Is(err, iamErrorx.ErrAdminRotationLockBusy) {
			iamMetrics.ObserveAdminKeyRotationOutcome(iamMetrics.AdminRotationOutcomeLockContention)
			logger.SysInfoFields(op, "rotation scheduler lock contention", logger.Fields{"run_id": runID, "result": "noop", "reason": "lock_busy"})
			continue
		}
		if errors.Is(err, iamErrorx.ErrAdminRotationDelivery) {
			iamMetrics.ObserveAdminKeyRotationOutcome(iamMetrics.AdminRotationOutcomeDeliveryFail)
		} else {
			iamMetrics.ObserveAdminKeyRotationOutcome(iamMetrics.AdminRotationOutcomeRotateFail)
		}

		if attempt < len(backoffSchedule)-1 {
			attempt++
		}
		retry := backoffSchedule[attempt]
		reason := iamMetrics.AdminRotationOutcomeRotateFail
		if errors.Is(err, iamErrorx.ErrAdminRotationDelivery) {
			reason = iamMetrics.AdminRotationOutcomeDeliveryFail
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
func (m *Module) runDeviceCapReconciler(ctx context.Context) {
	const op = "iam.device_cap.reconciler"
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
			if m == nil || m.authSvcImpl == nil {
				continue
			}
			processed, err := m.authSvcImpl.ReconcileDeviceCap(ctx, 100)
			if err != nil {
				iamMetrics.ObserveDeviceCapReconcile("error")
				logger.SysWarnFields(op, "reconcile failed", err, logger.Fields{})
				continue
			}
			if processed > 0 {
				logger.SysInfoFields(op, "reconcile fixed drift", logger.Fields{"processed_users": processed})
			}
			iamMetrics.ObserveDeviceCapReconcile("success")
		}
	}
}
