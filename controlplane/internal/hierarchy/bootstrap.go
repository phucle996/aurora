package hierarchy

import (
	"context"
	"fmt"

	"controlplane/pkg/logger"
)

// Bootstrap khởi tạo Shared Redis request handler của Hierarchy module.
func (m *Module) Bootstrap(_ context.Context) error {
	if m == nil || m.zoneRedis == nil {
		return fmt.Errorf("hierarchy bootstrap: zone Redis handler is required")
	}
	if err := m.zoneRedis.Start(); err != nil {
		return fmt.Errorf("hierarchy bootstrap: start zone Redis handler: %w", err)
	}
	logger.SysInfo("hierarchy.redis", "Successfully registered Shared Redis zone handlers")
	return nil
}
