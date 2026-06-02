// ============================================================================
// 📂 FILE: runtime/types/policy.go - Mô Hình Dữ Liệu runtime Toàn Cục Của Engine
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Đóng vai trò là điểm hội tụ (aggregation layer) cấu hình đã được biên dịch của
//     tất cả các phân hệ chính sách (Admin CIDR, Rate Limit, OpenTelemetry).
//   - Định nghĩa struct `RuntimePolicies` kiểu dữ liệu mạnh để chia sẻ trạng thái an toàn
//     cho toàn bộ hệ thống HTTP router, middleware và observability tracer.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Tổng hợp từ các package chính sách con dưới `policies/`.
//
// 🔒 RANH GIỚI BẢO MẬT/NGHIỆP VỤ (BOUNDARY):
//   - Đóng băng snapshot cấu hình hoạt động (Active Policy Set). Bất kỳ sự thay đổi nào
//     trong tệp YAML nguồn sẽ không ảnh hưởng trực tiếp đến runtime cho đến khi cơ chế
//     Hot-Swap hoán đổi nguyên tử (atomic swap) snapshot này thành công.
//
// 🔄 CALLSITE FLOW:
//   - `runtime/service.go` biên dịch ra `RuntimePolicies` và cập nhật vào `PolicySet`.
//   - Middleware và Tracing engine đọc các trường cụ thể trong `RuntimePolicies` thông qua
//     hàm `EngineService.Current()`.
//
// ============================================================================

package policytypes

import (
	"controlplane/internal/policyengine/policies/admincidr"
	"controlplane/internal/policyengine/policies/nats"
	"controlplane/internal/policyengine/policies/otel"
	"controlplane/internal/policyengine/policies/prometheus"
	"controlplane/internal/policyengine/policies/ratelimit"
	"time"
)

// PolicySet đại diện cho một Snapshot cấu hình hoàn chỉnh đang hoạt động.
// Mỗi khi reload thành công, một PolicySet mới sẽ được tạo ra nguyên tử.
type PolicySet struct {
	// Version là phiên bản của tệp chính sách (ví dụ: "v1").
	Version string

	// UpdatedAt ghi lại mốc thời gian snapshot được tải thành công vào bộ nhớ.
	UpdatedAt time.Time

	// Source là đường dẫn tương đối dẫn tới tệp nguồn cấu hình (ví dụ: "runtime/policies/policy.yaml").
	Source string

	// ChecksumSHA là chữ ký SHA256 của tệp YAML thô để phát hiện thay đổi nhanh chóng.
	ChecksumSHA string

	// Policies lưu các đối tượng chính sách thô dưới dạng generic map.
	Policies map[string]interface{}

	// Runtime chứa toàn bộ cấu hình chính sách đã biên dịch kiểu dữ liệu mạnh.
	Runtime RuntimePolicies
}

// RuntimePolicies đóng vai trò gom nhóm (aggregation) các chính sách runtime hoạt động.
type RuntimePolicies struct {
	// AdminCIDR chứa cấu hình an ninh phân vùng admin (Admin CIDR blocklist/allowlist).
	AdminCIDR admincidr.CompiledPolicy

	// RateLimit chứa cấu hình giới hạn tần suất yêu cầu (Token Bucket & Concurrency).
	RateLimit ratelimit.CompiledPolicy

	// OTel chứa cấu hình giám sát OpenTelemetry dynamic tracing.
	OTel otel.CompiledPolicy

	// Prometheus chứa cấu hình giám sát Prometheus dynamic metrics.
	Prometheus prometheus.CompiledPolicy

	// Nats chứa cấu hình mTLS xoay vòng nóng cho hạ tầng NATS.
	Nats nats.CompiledPolicy
}

// PolicySourceMeta mô tả siêu dữ liệu nguồn tệp tin phục vụ việc tối ưu hóa reload.
// Cho phép bỏ qua bước parse đắt đỏ nếu phiên bản và kích thước tệp nguồn không đổi.
type PolicySourceMeta struct {
	Path    string
	Version string
	Size    int64
}

// PolicyChangedEvent là cấu trúc sự kiện truyền bá gọn nhẹ trên Redis Pub/Sub Bus.
// Không bao giờ mang theo payload đầy đủ của policy nhằm tiết kiệm băng thông và tăng bảo mật.
type PolicyChangedEvent struct {
	Version          string
	Checksum         string
	SourceType       string
	EmittedAtUnixSec int64
	EmitterInstance  string
}
