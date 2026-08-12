package app

import (
	"context"
	"fmt"

	"controlplane/pkg/logger"
)

// RunModuleBootstraps is the single orchestration point for module bootstrap hooks.
//
// Contract:
// - This file belongs to the app layer and only coordinates module bootstrap order.
// - It must not contain module business logic, SQL, or secret-generation rules.
// - Each module is responsible for implementing its own Bootstrap() behavior.
// - App startup calls this after all modules are constructed and before serving traffic.
// - If a module has no bootstrap work, its Bootstrap() may safely no-op.
// - Bootstrap ordering between modules is explicit here when cross-module dependency exists.
func RunModuleBootstraps(ctx context.Context, modules *Modules) error {
	if modules == nil {
		return nil
	}

	// ------------------------------------------------------------------------
	// [BOOTSTRAP CACHE loaders] Đăng ký các loader tĩnh trước khi các module
	// bootstrap để sẵn sàng cung cấp dữ liệu qua L1 cache.
	// ------------------------------------------------------------------------
	if modules.CacheEngine != nil {
		RegisterL1Loaders(modules.CacheEngine, modules)
	}

	if modules.Hierarchy != nil {
		if err := modules.Hierarchy.Bootstrap(ctx); err != nil {
			return fmt.Errorf("app bootstrap: hierarchy module: %w", err)
		}
	}

	if modules.IAM != nil {
		if err := modules.IAM.Bootstrap(ctx); err != nil {
			logger.SysError("iam.bootstrap.apitoken", err.Error())
			return fmt.Errorf("app bootstrap: iam module: %w", err)
		}
	}

	if modules.ManagedService != nil {
		if err := modules.ManagedService.Bootstrap(ctx); err != nil {
			return fmt.Errorf("app bootstrap: managed service module: %w", err)
		}
	}

	// ------------------------------------------------------------------------
	// 🟢 HẠNG MỤC 2: KHỞI CHẠY TIER-1 (NON-CRITICAL MODULES)
	// ------------------------------------------------------------------------
	// SRE Note: Nếu quá trình khởi chạy ngầm (Bootstrap) của Hypervisor bị lỗi,
	// chúng ta chỉ ghi nhận log sự cố mà không chặn dòng khởi động toàn cục.
	if modules.Hypervisor != nil {
		if err := modules.Hypervisor.Bootstrap(ctx); err != nil {
			logger.SysError("hypervisor.bootstrap.failed", err.Error())
		}
	}

	if modules.Mail != nil {
		if err := modules.Mail.Start(ctx); err != nil {
			logger.SysError("mail.bootstrap.failed", err.Error())
		}
	}

	if modules.Storage != nil {
		if err := modules.Storage.Bootstrap(ctx); err != nil {
			return fmt.Errorf("app bootstrap: storage module: %w", err)
		}
	}

	return nil
}
