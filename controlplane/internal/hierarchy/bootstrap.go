// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/bootstrap.go
//            Khởi tạo các tác vụ chạy nền, đăng ký NATS/gRPC cho Core Module
// ======================================================================================================

package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	pubsubHandler "controlplane/internal/hierarchy/transport/pubsub/handler"
	"controlplane/pkg/logger"

	"google.golang.org/grpc"
)

// RegisterGRPCServices phơi ra phương thức đăng ký grpc services phục vụ app bootstrap layer.
// Do đã chuyển đổi hoàn toàn sang NATS Core, hàm này được giữ lại dưới dạng stub để tương thích.
func (m *Module) RegisterGRPCServices(server *grpc.Server) {
}

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

	// [COMMENT]: Khởi tạo sub-context riêng biệt để kiểm soát luồng background listener
	subCtx, cancel := context.WithCancel(ctx)
	m.listenCancel = cancel

	go func() {
		pubsub := m.rds.Subscribe(subCtx, "gateway:sync:requests")
		defer pubsub.Close()

		logger.SysInfo("hierarchy.pubsub", "Gateway sync requests listener started.")

		ch := pubsub.Channel()
		for {
			select {
			case <-subCtx.Done():
				logger.SysInfo("hierarchy.pubsub", "Gateway sync requests listener stopped (context done).")
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				logger.SysInfo("hierarchy.pubsub", fmt.Sprintf("Received sync request from edge: %s", msg.Payload))
				m.handleSyncRequest(subCtx, msg.Payload)
			}
		}
	}()

	return nil
}

// handleSyncRequest phân tích yêu cầu từ Edge, đọc database và publish ngược lại gateway:sync
func (m *Module) handleSyncRequest(ctx context.Context, payload string) {
	// Parse thủ công đơn giản: {"type": "zone", "code": "vn"}
	// Lọc code
	var code string
	if idx := strings.Index(payload, `"code"`); idx != -1 {
		sub := payload[idx:]
		if start := strings.Index(sub, `":"`); start != -1 {
			valSub := sub[start+3:]
			if end := strings.Index(valSub, `"`); end != -1 {
				code = valSub[:end]
			}
		}
	}
	if code == "" {
		return
	}

	// Đọc database lấy detail để sync
	if m.ZoneService != nil {
		zones, err := m.ZoneService.RPCListZones(ctx)
		if err == nil {
			for _, z := range zones {
				if z.Code == code {
					// Ghi lại Redis L2
					redisKey := fmt.Sprintf("zone:code:%s", z.Code)
					val := fmt.Sprintf("%s:%s", z.ID, z.Status)
					_ = m.rds.Set(ctx, redisKey, val, 24*time.Hour).Err()

					// Broadcast invalidation qua gateway:sync để Gateway reload
					_ = m.rds.Publish(ctx, "gateway:sync", fmt.Sprintf(`{"type": "zone", "code": "%s"}`, z.Code)).Err()
					logger.SysInfo("hierarchy.pubsub", fmt.Sprintf("Successfully responded and warmed up cache for zone: %s", code))
					return
				}
			}
		}
	}
}

// Stop hủy các background goroutine của module Core an toàn.
func (m *Module) Stop() {
	if m == nil {
		return
	}
	if m.listenCancel != nil {
		m.listenCancel()
		m.listenCancel = nil
	}

	// [COMMENT]: Hủy đăng ký NATS subscriptions trước khi tắt ứng dụng
	for _, sub := range m.natsSubs {
		if sub != nil {
			_ = sub.Unsubscribe()
		}
	}
	m.natsSubs = nil
}
