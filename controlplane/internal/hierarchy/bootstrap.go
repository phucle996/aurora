// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/bootstrap.go
//            Khởi tạo các tác vụ chạy nền, đăng ký NATS/gRPC cho Core Module
// ======================================================================================================

package core

import (
	"context"
	"fmt"

	pubsubHandler "controlplane/internal/hierarchy/transport/pubsub/handler"
	"controlplane/pkg/logger"
)

// Bootstrap khởi tạo các side-effect lâu dài và chạy các background task của module Core.
func (m *Module) Bootstrap(ctx context.Context) error {
	// [COMMENT]: Khởi động NATS subscriber để lắng nghe và điều phối luồng Zone qua NATS
	if m.natsConn != nil && m.ZoneService != nil {
		handler := pubsubHandler.NewZoneNatsHandler(m.cfg, m.ZoneService, m.otel)
		subs, err := handler.Subscribe(m.natsConn)
		if err != nil {
			return fmt.Errorf("hierarchy bootstrap: failed to subscribe NATS handler: %w", err)
		}
		m.natsSubs = append(m.natsSubs, subs...)
		logger.SysInfo("hierarchy.nats", "Successfully registered NATS zone handlers")
	}

	return nil
}
