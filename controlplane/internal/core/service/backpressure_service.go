// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/service/backpressure_service.go
//            Hiện Thực Hóa Logic Nghiệp Vụ Quản Trị Backpressure (Zone-Scoped)
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & DECOUPLED ZONE MODEL:
//   - Cài đặt chi tiết logic nghiệp vụ nhận thông báo quá tải (Backpressure) từ các Zone.
//   - Hệ thống hoạt động theo mô hình Fully Decoupled: Controlplane chỉ giao tiếp qua Redis Stream và
//     nhận dữ liệu giám sát backpressure theo Zone, không lưu vết hay heartbeat trực tiếp ở mức Node.
//
// ======================================================================================================

package coreSvcImpl

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreErrorx "controlplane/internal/core/taxonomy"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// backpressureCASScript thực hiện so khớp epoch nguyên tử (CAS) trên Redis để chống race condition
// khi nhiều job-proxy gửi tín hiệu backpressure đồng thời.
var backpressureCASScript = redis.NewScript(`
	local data_key = KEYS[1]
	local version_key = KEYS[2]
	local new_payload = ARGV[1]
	local new_version = ARGV[2]
	local ttl_sec = tonumber(ARGV[3])

	local existing_version = redis.call('GET', version_key)
	if existing_version then
		if tonumber(existing_version) >= tonumber(new_version) then
			-- Bác bỏ bản tin cũ hoặc bằng phiên bản hiện tại
			return 0
		end
	end

	redis.call('SET', data_key, new_payload, 'EX', ttl_sec)
	redis.call('SET', version_key, new_version, 'EX', ttl_sec)
	return 1
`)

// BackpressureService quản lý việc tiếp nhận và phát tán tín hiệu backpressure theo Zone.
type BackpressureService struct {
	l1Registry *cacheengine.CacheRegistry
}

// NewBackpressureService khởi tạo Service quản trị Backpressure ở dạng đơn giản hóa.
func NewBackpressureService(
	l1Registry *cacheengine.CacheRegistry,
) coreSvcInterface.BackpressureService {
	return &BackpressureService{
		l1Registry: l1Registry,
	}
}

// ReportBackpressure ghi nhận sự kiện nghẽn hàng đợi từ job-proxy và phát tán tới các replica qua Pub/Sub.
// Thiết lập cơ chế hai lớp: Ghi L2 (Redis-Core) để đồng bộ cho các node khởi động muộn (Bootstrapping),
// và phát Fanout để invalid/update L1 RAM Cache tức thời trên toàn cụm.
func (s *BackpressureService) ReportBackpressure(ctx context.Context, zoneID string, queueLen int64, pendingLen int64, congested bool, epoch int64, congestionRate float64) error {
	// Step 1: Kiểm tra tính hợp lệ của zoneID đầu vào
	parsedZoneID, err := uuid.Parse(strings.TrimSpace(zoneID))
	if err != nil {
		return coreErrorx.ErrZoneInvalidInput
	}

	key := "zone_backpressure:" + parsedZoneID.String()

	// Chuẩn bị payload thông tin quá tải chi tiết
	payload := map[string]interface{}{
		"zone_id":         parsedZoneID.String(),
		"queue_len":       queueLen,
		"pending_len":     pendingLen,
		"congested":       congested,
		"epoch":           epoch,
		"congestion_rate": congestionRate,
		"timestamp":       time.Now().Unix(),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	shouldPublish := true

	if s.l1Registry != nil && s.l1Registry.L2 != nil {
		rdb := s.l1Registry.L2.Client()
		if rdb != nil {
			// 1. Chạy nguyên tử script Lua so khớp Epoch (CAS) chống race condition
			dataKey := "{" + key + "}:data"
			versionKey := "{" + key + "}:version"

			res, err := backpressureCASScript.Run(ctx, rdb, []string{dataKey, versionKey}, string(payloadBytes), strconv.FormatInt(epoch, 10), 30).Result()
			if err != nil {
				logger.SysErrorFields("core.backpressure", "Không thể chạy script CAS backpressure", err, logger.Fields{"zone": zoneID})
				return err
			}

			// Nếu kết quả trả về là 0 -> Epoch bị stale, không cập nhật và không phát tán Fanout
			if resVal, ok := res.(int64); ok && resVal == 0 {
				logger.SysInfoFields("core.backpressure", "Bỏ qua bản tin backpressure do epoch cũ hơn dữ liệu hiện tại", logger.Fields{
					"zone":  zoneID,
					"epoch": epoch,
				})
				shouldPublish = false
			}
		} else {
			// Môi trường mock/test: dùng hàm Set thông thường
			err = s.l1Registry.L2.Set(ctx, key, payload, epoch, 30*time.Second)
			if err != nil {
				logger.SysErrorFields("core.backpressure", "Không thể ghi nhận trạng thái nghẽn lên L2 cache", err, logger.Fields{"zone": zoneID})
			}
		}
	}

	// 2. Bắn sự kiện Fanout để dọn RAM L1 Cache cục bộ của các replica khác khi dữ liệu thay đổi hợp lệ
	if shouldPublish && s.l1Registry != nil && s.l1Registry.Fanout != nil {
		detachedCtx := context.WithoutCancel(ctx)
		go func() {
			if _, err := s.l1Registry.Fanout.Publish(detachedCtx, key, payloadBytes); err != nil {
				logger.SysWarnFields("core.backpressure", "Không thể phát tán sự kiện Fanout quá tải", err, logger.Fields{"zone": zoneID})
			}
		}()
	}

	return nil
}
