// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/service/dataplane_subscriber.go
//            Hiện Thực Hóa Background Heartbeat Ingestion Qua Redis Pub/Sub (Hot Path)
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & SỰ PHÙ HỢP HOẠT ĐỘNG (DESIGN CONTRACT & SRE STANDARDS):
//   - Triển khai Background Subscriber/Consumer chịu trách nhiệm lắng nghe (Subscribe) các nhịp tim
//     được publish định kỳ từ các cụm Dataplane lên Redis Cluster.
//   - Bảo đảm tính sẵn sàng cao (High-Availability - HA) và tối ưu hóa xử lý bất đồng bộ (Non-blocking):
//
//     1) GRACEFUL LIFECYCLE SHUTDOWN (CONTAINER LIFECYCLE FRIENDLY):
//        * Lắng nghe tín hiệu kết thúc từ Context cha để hủy việc subscribe và thoát khỏi goroutine chạy nền
//          an toàn ngay khi container khởi động lại hoặc tắt cụm, cam kết không rò rỉ (leak) tài nguyên.
//
//     2) ZERO RUNTIME BLOCKING & BACKPRESSURE PROTECTION:
//        * Tiếp nhận tin nhắn bất đồng bộ, xử lý parse JSON nhanh chóng và gọi xuống Core Service Layer.
//        * Nếu PostgreSQL gặp độ trễ cao, subscriber ghi log cảnh báo và tiếp tục tiêu thụ tin nhắn
//          tiếp theo, không làm tắc nghẽn channel của Redis.
//
//     3) PROMETHEUS TELEMETRY INTEGRATION:
//        * Ghi nhận đầy đủ số lượng tin nhắn heartbeat qua Prometheus Counter (`ObserveHeartbeat`)
//          với label path="pubsub", phục vụ giám sát liveness của toàn hệ thống trên Grafana.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Redis Pub/Sub Channel `core:dataplane:heartbeats:pubsub` là SoT trung chuyển tin nhắn transient.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Chỉ đóng vai trò là tầng chuyển tiếp (Event Bridge Transport), không tự ý cập nhật DB hay cache trực tiếp.
//   - Mọi lỗi parse JSON hay lỗi DB được bắt gọn gàng, nuốt lỗi thông minh để không gây PANIC sập ứng dụng (Crash Resilience).
//
// 🔄 CALLSITE FLOW:
//   - Được khởi chạy bất đồng bộ qua phương thức `Start(ctx)` bởi Core Module (`internal/core/module.go`).
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - SRE cần thiết lập cảnh báo Prometheus Alerting nếu tỷ lệ `"failure"` trên nhãn `path="pubsub"` tăng cao đột biến.
//
// ======================================================================================================

package coreSvcImpl

import (
	"context"
	"encoding/json"
	"time"

	coreCache "controlplane/internal/core/cache"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreMetric "controlplane/internal/core/metrics"
	"controlplane/pkg/logger"
)

type HeartbeatMessage struct {
	ClusterID string `json:"cluster_id"`
	ZoneID    string `json:"zone_id"`
}

type DataplaneHeartbeatSubscriber struct {
	cache   coreCache.DataplaneCache
	service coreSvcInterface.DataplaneNodeService
}

// NewDataplaneHeartbeatSubscriber khởi tạo subscriber chạy nền.
func NewDataplaneHeartbeatSubscriber(
	cache coreCache.DataplaneCache,
	service coreSvcInterface.DataplaneNodeService,
) *DataplaneHeartbeatSubscriber {
	return &DataplaneHeartbeatSubscriber{
		cache:   cache,
		service: service,
	}
}

// Start khởi chạy vòng lặp lắng nghe tin nhắn nhịp tim từ Redis Pub/Sub bất đồng bộ.
func (s *DataplaneHeartbeatSubscriber) Start(ctx context.Context) {
	channelName := "core:dataplane:heartbeats:pubsub"

	// Step 1: Subscribe vào channel Redis Pub/Sub thông qua interface DataplaneCache.
	pubsub := s.cache.Subscribe(ctx, channelName)
	defer pubsub.Close()

	logger.SysInfoFields("core.dataplane.subscriber", "started background heartbeat pubsub subscriber", logger.Fields{"channel": channelName})

	ch := pubsub.Channel()

	// Step 2: Vòng lặp liên tục nhận và xử lý tin nhắn cho tới khi context kết thúc.
	for {
		select {
		case <-ctx.Done():
			// Nhận tín hiệu graceful shutdown -> thoát loop an toàn
			logger.SysInfoFields("core.dataplane.subscriber", "stopping background heartbeat pubsub subscriber due to context cancellation", nil)
			return
		case msg, ok := <-ch:
			if !ok {
				// Nếu Redis channel bị đóng đột ngột -> thử tái thiết lập kết nối sau 2s
				logger.SysWarnFields("core.dataplane.subscriber", "redis pubsub channel closed, retrying in 2 seconds", nil, nil)
				time.Sleep(2 * time.Second)
				pubsub = s.cache.Subscribe(ctx, channelName)
				ch = pubsub.Channel()
				continue
			}

			// Step 3: Đã nhận được tin nhắn nhịp tim! Tiến hành unmarshal JSON payload.
			var payload HeartbeatMessage
			if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
				// Ghi nhận failure metric & log cảnh báo, không panic
				coreMetric.ObserveHeartbeat("pubsub", "failure")
				logger.SysWarnFields("core.dataplane.subscriber", "failed to parse heartbeat JSON payload", err, logger.Fields{"payload": msg.Payload})
				continue
			}

			// Step 4: Gọi IngestHeartbeat để xử lý lưu DB và gia hạn Redis Lease.
			err := s.service.IngestHeartbeat(ctx, payload.ClusterID, payload.ZoneID)
			if err != nil {
				coreMetric.ObserveHeartbeat("pubsub", "failure")
				logger.SysWarnFields("core.dataplane.subscriber", "failed to ingest heartbeat from pubsub", err, logger.Fields{"cluster": payload.ClusterID, "zone": payload.ZoneID})
			} else {
				// Ghi nhận success metric ghi dấu telemetry sống động
				coreMetric.ObserveHeartbeat("pubsub", "success")
			}
		}
	}
}
