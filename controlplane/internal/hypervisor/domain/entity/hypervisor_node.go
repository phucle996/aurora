package hypervisorEntity

import (
	"time"

	"github.com/google/uuid"
)

// HypervisorNode là Domain Entity đại diện cho máy chủ ảo hóa vật lý Proxmox.
// Chứa các logic nghiệp vụ thuần túy và độc lập hoàn toàn với cơ sở dữ liệu hay giao thức REST HTTP.
type HypervisorNode struct {
	ID       uuid.UUID // ID định danh duy nhất (UUIDv7)
	ZoneID   uuid.UUID // ID phân vùng Zone vật lý
	NodeCode string    // Mã định danh vật lý (ví dụ: pve-node-01)
	Name     string    // Tên hiển thị thân thiện
	Status   string    // Trạng thái (connected, disconnected, degraded, maintenance)

	// Các thông số dung lượng tài nguyên (Capacity metrics)
	CPUCoresTotal  int
	CPUCoresUsed   int
	RAMMBTotal     int64
	RAMMBUsed      int64
	StorageGBTotal int64
	StorageGBUsed  int64

	// Metadata vận hành
	LastActiveAt time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
