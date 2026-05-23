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

	if modules.Core != nil {
		if err := modules.Core.Bootstrap(ctx); err != nil {
			return fmt.Errorf("app bootstrap: core module: %w", err)
		}
	}

	if modules.IAM != nil {
		if err := modules.IAM.Bootstrap(ctx); err != nil {
			logger.SysError("iam.bootstrap.apitoken", err.Error())
			return fmt.Errorf("app bootstrap: iam module: %w", err)
		}
	}

	return nil
}
