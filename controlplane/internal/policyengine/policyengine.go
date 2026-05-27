// ============================================================================
// 📂 FILE: policyengine.go - Điểm Khởi Tạo Hệ Thống Chính Sách (Engine Entrypoint)
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Đóng vai trò là "Cổng vào chính" (Main Entrypoint) và bộ nạp (Bootstrap) của
//     phân hệ Policy Engine cho toàn bộ Aurora Controlplane.
//   - Quản lý vòng đời khởi tạo (Instantiation) và giải phóng tài nguyên (Lifecycle Cleanup)
//     của các tiến trình quét cấu hình chạy ngầm (Background Worker Goroutines).
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Khởi chạy luồng nạp cấu hình động đầu tiên từ tệp tin `runtime/policies/policy.yaml`.
//
// 🔒 RANH GIỚI BẢO MẬT/NGHIỆP VỤ (BOUNDARY):
//   - Ranh giới khởi tạo (Bootstrap boundary): Thực thi kiểm tra Fail-Fast đối với các
//     phụ thuộc hạ tầng thiết yếu (như Redis Client). Nếu thiếu, hệ thống sẽ dừng ngay lập tức
//     (panic/return error) để bảo vệ tính toàn vẹn của Controlplane.
//   - Cô lập Goroutine: Kiểm soát triệt để tài nguyên chạy nền bằng cơ chế `context.CancelFunc`
//     tránh rò rỉ bộ nhớ (goroutine leaks) khi Controlplane tắt.
//
// 🔄 CALLSITE FLOW:
//   - Được gọi duy nhất một lần tại tệp khởi tạo ứng dụng [app.go](file:///home/phucle/Desktop/New/controlplane/internal/app/app.go)
//     trong hàm dựng đồ thị phụ thuộc (dependency graph) của Server.
//
// ============================================================================

package policyengine

import (
	"context"
	"errors"

	"controlplane/internal/config"
	policyAdapter "controlplane/internal/policyengine/adapter"
	policyruntime "controlplane/internal/policyengine/runtime"

	goredis "github.com/redis/go-redis/v9"
)

// Engine đại diện cho dịch vụ hạ tầng quản lý chính sách toàn hệ thống.
type Engine struct {
	// EngineService là lõi điều phối xử lý logic hot-swap và sync của Engine.
	EngineService *policyruntime.EngineService
	
	// workerCancel là hàm hủy bỏ để dừng sạch sẽ toàn bộ luồng chạy ngầm của các worker.
	workerCancel  context.CancelFunc
}

// New khởi tạo cấu hình, liên kết Redis Pub/Sub, nạp Adapter và khởi chạy các tiến trình nền.
//
// # Tham số:
//   - `cfg`: Cấu hình hệ thống toàn cục.
//   - `rds`: Kết nối Redis phục vụ đồng bộ hóa đa instance (Cross-instance propagation).
//
// # Trả về:
//   - Con trỏ `Engine`: Thực thể điều khiển của Policy Engine.
//   - `error`: Lỗi nếu không đủ điều kiện khởi chạy (ví dụ: thiếu kết nối Redis).
func New(cfg *config.Config, rds *goredis.Client) (*Engine, error) {
	// Kiểm tra Fail-fast: Redis là thành phần bắt buộc để truyền bá sự thay đổi cấu hình.
	if rds == nil {
		return nil, errors.New("policyengine: redis client is required")
	}
	
	// Tạo Adapter đọc file YAML nội bộ
	source := policyAdapter.NewYAMLFileSourceAdapter("runtime/policies/policy.yaml")
	if source == nil {
		return nil, errors.New("policyengine: source adapter is required")
	}
	
	// Khởi tạo kênh thông báo và lắng nghe qua Redis Pub/Sub
	notifier := policyruntime.NewRedisPubSubNotifier(rds, "policyengine.policy.changed.v1")
	if notifier == nil {
		return nil, errors.New("policyengine: propagation notifier is required")
	}
	
	subscriber := notifier
	service := policyruntime.NewEngineService(cfg, source, notifier, subscriber)
	if service == nil {
		return nil, errors.New("policyengine: engine service is required")
	}
	
	// Thiết lập context điều phối vòng đời của các Goroutines chạy nền
	workerCtx, workerCancel := context.WithCancel(context.Background())
	service.Start(workerCtx)
	
	// Thực hiện nạp cấu hình và biên dịch lần đầu (Initial sync load).
	// Nếu tệp cấu hình không hợp lệ ngay từ lúc khởi động, Controlplane sẽ từ chối chạy.
	if _, err := service.Reload(workerCtx); err != nil {
		workerCancel()
		return nil, err
	}
	
	return &Engine{
		EngineService: service,
		workerCancel:  workerCancel,
	}, nil
}

// Stop dừng an toàn toàn bộ các Goroutines chạy ngầm của Policy Engine.
// Hàm này là Nil-safe để đảm bảo gọi nhiều lần không phát sinh lỗi hoặc panic.
func (e *Engine) Stop() {
	if e == nil {
		return
	}
	if e.workerCancel != nil {
		e.workerCancel()
		e.workerCancel = nil
	}
}
