/*
Package coreReq định nghĩa các DTO cho logical zone subdomain.

DESIGN CONTRACTS:
- Zone mutations (như update status) dùng hoàn toàn DTO payload body thay vì URI path parameters.
- Chuẩn hóa route thành PATCH /admin/core/zones/status giúp đồng bộ edge gateway và logging.

SOURCE OF TRUTH:
- Định nghĩa struct ở đây là Source of Truth cho zone handler layer, map trực tiếp từ request body của client.
- Bắt buộc đồng bộ với admin-ui và runbook-web.

SYSTEM BOUNDARY:
- Input Validation: Dùng binding:"required,uuid" để xác thực dữ liệu tại HTTP transport boundary.
- Type Parsing: Tự động parse chuỗi JSON sang uuid.UUID để đảm bảo type safety trước khi xử lý.
- Service Layer Handoff: Bàn giao dữ liệu hợp lệ về cú pháp cho domain service layer xử lý logic nghiệp vụ.
*/
package coreReq

import "github.com/google/uuid"

// CreateZoneRequest định nghĩa dữ liệu đầu vào cần thiết để khởi tạo một datacenter/logical zone mới.
type CreateZoneRequest struct {
	// Code đại diện cho mã định danh duy nhất và ngắn gọn của zone (ví dụ: "us-east-1").
	Code string `json:"code"              binding:"required"`
	// Name đại diện cho tên mô tả dễ đọc của zone (ví dụ: "North Virginia").
	Name string `json:"name"              binding:"required"`
	// Location xác định vị trí vật lý/địa lý của zone.
	Location string `json:"location"`
	// EnableHypervisor xác định xem các dịch vụ compute KVM có được kích hoạt khi cài đặt hay không.
	EnableHypervisor *bool `json:"enable_hypervisor"`
	// EnableStorage xác định xem các dịch vụ lưu trữ (storage block/volume) có được kích hoạt khi cài đặt hay không.
	EnableStorage *bool `json:"enable_storage"`
	// EnableMail xác định xem dịch vụ SMTP và delivery workers có được kích hoạt khi cài đặt hay không.
	EnableMail *bool `json:"enable_mail"`
	// EnableK8s xác định xem dịch vụ managed Kubernetes clusters có được kích hoạt khi cài đặt hay không.
	EnableK8s *bool `json:"enable_k8s"`
	// EnableAI xác định xem dịch vụ compute hiệu năng cao GPU/AI clusters có được kích hoạt khi cài đặt hay không.
	EnableAI *bool `json:"enable_ai"`
}

// UpdateZoneStatusRequest định nghĩa dữ liệu đầu vào để cập nhật trạng thái hoạt động của một zone.
type UpdateZoneStatusRequest struct {
	// ZoneID là mã định danh của zone cần cập nhật. Yêu cầu định dạng UUID hợp lệ và không rỗng.
	ZoneID uuid.UUID `json:"zone_id" binding:"required,uuid"`
	// Status đại diện cho trạng thái tiếp theo cần chuyển đổi tới (ví dụ: "active", "draining", "maintenance").
	Status string `json:"status"  binding:"required"`
}

// UpsertZoneServiceRequest định nghĩa dữ liệu đầu vào để kích hoạt hoặc hủy kích hoạt một dịch vụ/module trong zone.
type UpsertZoneServiceRequest struct {
	// ZoneID là mã định danh của zone cần cập nhật. Yêu cầu định dạng UUID hợp lệ và không rỗng.
	ZoneID uuid.UUID `json:"zone_id" binding:"required,uuid"`
	// ServiceType đại diện cho loại dịch vụ (ví dụ: "hypervisor", "storage", "kubernetes", "smtp").
	ServiceType string `json:"service_type" binding:"required"`
	// Enabled xác định trạng thái mong muốn của dịch vụ (kích hoạt/hủy kích hoạt).
	Enabled *bool `json:"enabled"      binding:"required"`
}
