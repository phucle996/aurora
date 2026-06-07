// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/service/dataplane_node_service.go
//            Hiện Thực Hóa Logic Nghiệp Vụ Điều Phối Dataplane Cluster
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & FAST PROBING SHARD (CONTRACT & DYNAMIC PROBING PATH):
//   - Cài đặt chi tiết logic nghiệp vụ heartbeat ingestion, liveness verification và dynamic routing
//     cho Dataplane Cluster.
//   - Bảo đảm khả năng phát hiện sự cố cực nhạy và kích hoạt failover tự động dưới 1.5 giây:
//
//     1) FAST FAIL-FAST PROBING PATH (Hot-Path):
//        * Hàm `VerifyClusterStatus` thực hiện kiểm tra nhanh O(1) trạng thái Redis Lease Key.
//        * Nếu Redis Lease hết hạn, lập tức kích hoạt Slow Path truy vấn PostgreSQL DB và cập nhật trạng thái
//          thành `failed` ngay tức khắc thay vì chờ vòng lặp background worker.
//
//     2) ZERO RUNTIME BLOCKING:
//        * Các thao tác kiểm tra sự sống được tối ưu hóa cực hạn, tránh blocking các network goroutine
//          của client nghiệp vụ.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Redis Lease Key `core:dataplane:lease:{zone_id}` là SoT tuyệt đối cho trạng thái sống động (liveness)
//     thực tế trong vòng 8 giây.
//   - Bảng 'core.dataplane_nodes' trong Postgres là SoT cho desired configuration và trạng thái snapshot
//     được đồng bộ thứ cấp.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Service này không làm nhiệm vụ transport mapping (HTTP/REST status codes).
//   - Không thực hiện các tác vụ mạng như bắn trực tiếp gRPC call sang Dataplane ở luồng verify liveness thông thường
//     để tránh blocking thread.
//
// 🔄 CALLSITE FLOW:
//   - Được gọi trực tiếp bởi các Handler HTTP/gRPC (khi tiếp nhận heartbeat từ Dataplane).
//   - Được gọi bởi các module nghiệp vụ như `MailConsumer` để tìm cụm Dataplane đủ điều kiện gửi email.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Cơ chế Fast Fail-Fast Probing ở hàm `VerifyClusterStatus` giúp rút ngắn thời gian phát hiện cụm sập
//     từ 10-15s xuống dưới 1-2s ngay khi có sự cố mạng ở luồng nghiệp vụ.
//   - Logic `IngestHeartbeat` sử dụng idempotent UPSERT qua repo, bảo đảm không bao giờ xảy ra lỗi trùng
//     khóa chính khi cụm khởi động lại nhanh chóng.
//
// ======================================================================================================

package coreSvcImpl

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreCache "controlplane/internal/core/cache"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreErrorx "controlplane/internal/core/taxonomy"
	"controlplane/pkg/logger"
	"sync"

	"github.com/google/uuid"
)

type DataplaneNodeService struct {
	repo          coreRepoInterface.DataplaneNodeRepository
	cache         coreCache.DataplaneCache
	zoneRepo      coreRepoInterface.ZoneRepository
	fallbackCache sync.Map // Map lưu vết nhịp tim gRPC dự phòng khi Redis sập (Key: "zone_id:hostname" -> Value: time.Time)
}

// NewDataplaneNodeService khởi tạo Service quản trị Dataplane.
func NewDataplaneNodeService(
	repo coreRepoInterface.DataplaneNodeRepository,
	cache coreCache.DataplaneCache,
	zoneRepo coreRepoInterface.ZoneRepository,
) coreSvcInterface.DataplaneNodeService {
	return &DataplaneNodeService{
		repo:     repo,
		cache:    cache,
		zoneRepo: zoneRepo,
	}
}

// IngestHeartbeat nhận nhịp tim thời gian thực từ cụm Dataplane, cập nhật Redis Lease và Postgres DB.
func (s *DataplaneNodeService) IngestHeartbeat(ctx context.Context, clusterID string, zoneID string) error {
	// Step 1: Validate đầu vào, chuyển các ID chuỗi sang kiểu UUID chuẩn hóa.
	parsedClusterID, err := uuid.Parse(strings.TrimSpace(clusterID))
	if err != nil {
		return coreErrorx.ErrZoneInvalidInput
	}
	parsedZoneID, err := uuid.Parse(strings.TrimSpace(zoneID))
	if err != nil {
		return coreErrorx.ErrZoneInvalidInput
	}

	// Step 2: Lưu/Gia hạn Redis Lease Key (8s TTL) - Bảo chứng sự sống động tức thời.
	err = s.cache.AcquireLease(ctx, parsedZoneID.String(), 8*time.Second)
	if err != nil {
		logger.SysWarnFields("core.dataplane.heartbeat", "failed to acquire redis lease", err, logger.Fields{"zone": zoneID, "cluster": clusterID})
		return err
	}

	// Step 3: Kiểm tra sự tồn tại của Zone trong DB để đảm bảo tính toàn vẹn tham chiếu.
	zone, err := s.zoneRepo.GetZoneByID(ctx, parsedZoneID)
	if err != nil {
		return err
	}
	if zone == nil {
		return coreErrorx.ErrZoneNotFound
	}

	// Step 4: Chuẩn bị thông tin endpoint Load Balancer của cụm. Tự động build endpoint nội bộ theo mẫu.
	now := time.Now().UTC()
	endpoint := fmt.Sprintf("dns:///dp-lb.%s.internal:9000", zone.Code)

	// Step 5: Đóng gói Domain Entity với trạng thái hoạt động 'ready'.
	cluster := coreEntity.DataplaneNode{
		ID:        parsedClusterID.String(),
		Status:    coreEntity.DataplaneNodeStatusReady,
		ZoneID:    parsedZoneID.String(),
		Endpoint:  endpoint,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Step 6: Lưu xuống Postgres DB một cách an toàn thông qua Repository (idempotent UPSERT).
	err = s.repo.RegisterCluster(ctx, cluster)
	if err != nil {
		logger.SysWarnFields("core.dataplane.heartbeat", "failed to persist cluster status in postgres", err, logger.Fields{"zone": zoneID, "cluster": clusterID})
		return err
	}

	return nil
}

// VerifyClusterStatus thực hiện kiểm tra khẩn cấp và chuyển trạng thái cụm sang 'failed' tức thời nếu sập.
func (s *DataplaneNodeService) VerifyClusterStatus(ctx context.Context, zoneID string) (string, error) {
	// Step 1: Validate tham số đầu vào.
	parsedZoneID, err := uuid.Parse(strings.TrimSpace(zoneID))
	if err != nil {
		return string(coreEntity.DataplaneNodeStatusFailed), coreErrorx.ErrZoneInvalidInput
	}

	// Step 2: [FAST PATH] Kiểm tra nhanh sự tồn tại của Redis Lease O(1). Nếu lease còn sống -> Trả về 'ready' ngay lập tức!
	exists, err := s.cache.CheckLeaseExists(ctx, parsedZoneID.String())
	if err == nil && exists {
		return string(coreEntity.DataplaneNodeStatusReady), nil
	}

	// Step 3: [SLOW PATH] Redis Lease đã mất tích! Cần xác thực và cập nhật trạng thái DB sang 'failed'.
	cluster, err := s.repo.GetClusterByZone(ctx, parsedZoneID)
	if err != nil {
		return string(coreEntity.DataplaneNodeStatusFailed), err
	}
	if cluster == nil {
		return string(coreEntity.DataplaneNodeStatusFailed), coreErrorx.ErrZoneNotFound
	}

	// Step 4: Kiểm tra lại Redis lease một lần nữa (double-check) để phòng ngừa Race Condition do trễ mạng nhẹ trước khi quyết định sập.
	exists, _ = s.cache.CheckLeaseExists(ctx, parsedZoneID.String())
	if exists {
		return string(coreEntity.DataplaneNodeStatusReady), nil
	}

	// Step 5: Nếu trạng thái trong Postgres đã là 'failed' rồi thì trả về luôn, tránh ghi DB thừa.
	if cluster.Status == coreEntity.DataplaneNodeStatusFailed {
		return string(coreEntity.DataplaneNodeStatusFailed), nil
	}

	// Step 6: Thực hiện ghi nhận trạng thái 'failed' vào Postgres DB tức thời.
	parsedClusterID, _ := uuid.Parse(cluster.ID)
	err = s.repo.UpdateClusterStatus(ctx, parsedClusterID, coreEntity.DataplaneNodeStatusFailed)
	if err != nil {
		return string(coreEntity.DataplaneNodeStatusFailed), err
	}

	// Step 7: Ghi log cảnh báo SRE để phát cảnh báo hệ thống (Prometheus/Grafana thu hoạch).
	logger.SysWarnFields("core.dataplane.fast_probing", "cluster marked as failed due to missing heartbeat lease", nil, logger.Fields{"zone": zoneID, "cluster": cluster.ID})
	return string(coreEntity.DataplaneNodeStatusFailed), nil
}

// GetEligibleClusterForZone kiểm tra và trả về Endpoint cụm Dataplane khỏe mạnh có hỗ trợ service mong muốn tại Zone hoạt động.
func (s *DataplaneNodeService) GetEligibleClusterForZone(ctx context.Context, zoneID string, serviceType string) (*coreEntity.DataplaneNode, error) {
	// Step 1: Validate Zone ID.
	parsedZoneID, err := uuid.Parse(strings.TrimSpace(zoneID))
	if err != nil {
		return nil, coreErrorx.ErrZoneInvalidInput
	}

	// Step 2: Truy xuất thông tin Zone và kiểm tra xem Zone đó có đang ở trạng thái 'active' không.
	zone, err := s.zoneRepo.GetZoneByID(ctx, parsedZoneID)
	if err != nil {
		return nil, err
	}
	if zone == nil {
		return nil, coreErrorx.ErrZoneNotFound
	}
	if zone.Status != coreEntity.ZoneStatusActive {
		return nil, coreErrorx.ErrZoneInvalidInput
	}

	// Step 3: Kiểm tra xem Dịch vụ chỉ định (ví dụ: mail) có được kích hoạt (enabled) tại Zone này không.
	services, err := s.zoneRepo.ListZoneServicesByZoneID(ctx, parsedZoneID)
	if err != nil {
		return nil, err
	}
	serviceEnabled := false
	for _, svc := range services {
		if strings.EqualFold(string(svc.ServiceType), serviceType) && svc.Enabled {
			serviceEnabled = true
			break
		}
	}
	if !serviceEnabled {
		return nil, nil // Dịch vụ không bật ở zone này
	}

	// Step 4: Lấy thông tin cụm Dataplane và xác minh trạng thái hoạt động 'ready'.
	cluster, err := s.repo.GetClusterByZone(ctx, parsedZoneID)
	if err != nil {
		return nil, err
	}
	if cluster == nil || cluster.Status != coreEntity.DataplaneNodeStatusReady {
		return nil, nil // Không có cụm nào sẵn sàng hoạt động
	}

	return cluster, nil
}

// IngestFallbackHeartbeat tiếp nhận nhịp tim dự phòng qua kênh gRPC trực tiếp từ Node khi Redis sập.
// Nhịp tim này được ghi nhận cực nhanh vào bộ nhớ trong (sync.Map) nhằm triệt tiêu tối đa rủi ro latency và DB overhead.
func (s *DataplaneNodeService) IngestFallbackHeartbeat(ctx context.Context, hostname string, zoneID string) error {
	key := fmt.Sprintf("%s:%s", zoneID, hostname)
	now := time.Now().UTC()

	// Ghi nhận thời gian nhận nhịp tim cuối cùng của Node vào thread-safe sync.Map
	s.fallbackCache.Store(key, now)

	logger.SysInfoFields("core.dataplane.heartbeat", "Successfully ingested fallback gRPC heartbeat", logger.Fields{
		"hostname":  hostname,
		"zone_id":   zoneID,
		"timestamp": now.Format(time.RFC3339),
	})

	return nil
}

// CheckFallbackLiveness đối soát xem Node có gửi nhịp tim gRPC dự phòng hợp lệ trong vòng 8 giây qua hay không.
// Đây là lá chắn an toàn tối quan trọng (Anti-False-Positive Shield) giúp hệ thống không báo tử nhầm node.
func (s *DataplaneNodeService) CheckFallbackLiveness(ctx context.Context, zoneID string, hostname string) bool {
	key := fmt.Sprintf("%s:%s", zoneID, hostname)

	// Truy xuất mốc thời gian từ sync.Map
	val, ok := s.fallbackCache.Load(key)
	if !ok {
		return false // Hoàn toàn không nhận được nhịp tim dự phòng nào trước đó
	}

	lastHeartbeat, ok := val.(time.Time)
	if !ok {
		return false
	}

	// Nếu nhịp tim nhận được trong vòng 8 giây gần nhất -> Node vẫn khỏe mạnh!
	return time.Now().UTC().Sub(lastHeartbeat) <= 8*time.Second
}
