// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/bootstrap.go
//            Khởi tạo các tác vụ chạy nền, đăng ký NATS handler cho Core Module
//            Transport: NATS — không dùng gRPC trực tiếp
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
	// [COMMENT]: Đăng ký độc lập từng NATS Handler phục vụ đồng bộ và phân giải Zone
	if m.natsConn != nil && m.ZoneService != nil {
		handler := pubsubHandler.NewZoneNatsHandler(m.cfg, m.ZoneService, m.otel)
		const queueGroup = "hierarchy_zone_service"

		// 1. Luồng đồng bộ danh sách Zones (GetZoneList) — NATS subject chuẩn hóa
		subGetList, err := m.natsConn.QueueSubscribe("hierarchy.zone.get_zone_list", queueGroup, handler.HandleGetZoneList)
		if err != nil {
			return fmt.Errorf("hierarchy bootstrap: failed to subscribe HandleGetZoneList: %w", err)
		}
		m.natsSubs = append(m.natsSubs, subGetList)

		// 2. Luồng phân giải Zone (ResolveZone) — NATS subject chuẩn hóa
		subResolve, err := m.natsConn.QueueSubscribe("hierarchy.zone.resolve_zone", queueGroup, handler.HandleResolveZone)
		if err != nil {
			return fmt.Errorf("hierarchy bootstrap: failed to subscribe HandleResolveZone: %w", err)
		}
		m.natsSubs = append(m.natsSubs, subResolve)

		logger.SysInfo("hierarchy.nats", "Successfully registered NATS zone handlers")
	}

	return nil
}
