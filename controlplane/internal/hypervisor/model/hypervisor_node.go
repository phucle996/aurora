package hypervisorModel

import (
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"

	"github.com/google/uuid"
)

// HypervisorNode đại diện cho cấu trúc bảng CSDL 'nodes' trong PostgreSQL.
// Chỉ chứa duy nhất các tag 'db' phục vụ mapping dữ liệu với SQL Driver/ORM.
type HypervisorNode struct {
	ID             uuid.UUID `db:"id"`
	ZoneID         uuid.UUID `db:"zone_id"`
	NodeCode       string    `db:"node_code"`
	Name           string    `db:"name"`
	Status         string    `db:"status"`
	CPUCoresTotal  int       `db:"cpu_cores_total"`
	CPUCoresUsed   int       `db:"cpu_cores_used"`
	RAMMBTotal     int64     `db:"ram_mb_total"`
	RAMMBUsed      int64     `db:"ram_mb_used"`
	StorageGBTotal int64     `db:"storage_gb_total"`
	StorageGBUsed  int64     `db:"storage_gb_used"`
	LastActiveAt   time.Time `db:"last_active_at"`
	CreatedAt      time.Time `db:"created_at"`
	UpdatedAt      time.Time `db:"updated_at"`
}

// NodeEntityToModel chuyển đổi từ Domain Entity sang DB Model.
func NodeEntityToModel(e hypervisorEntity.HypervisorNode) HypervisorNode {
	return HypervisorNode{
		ID:             e.ID,
		ZoneID:         e.ZoneID,
		NodeCode:       e.NodeCode,
		Name:           e.Name,
		Status:         e.Status,
		CPUCoresTotal:  e.CPUCoresTotal,
		CPUCoresUsed:   e.CPUCoresUsed,
		RAMMBTotal:     e.RAMMBTotal,
		RAMMBUsed:      e.RAMMBUsed,
		StorageGBTotal: e.StorageGBTotal,
		StorageGBUsed:  e.StorageGBUsed,
		LastActiveAt:   e.LastActiveAt,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}
}

// NodeModelToEntity chuyển đổi từ DB Model sang Domain Entity.
func NodeModelToEntity(m HypervisorNode) hypervisorEntity.HypervisorNode {
	return hypervisorEntity.HypervisorNode{
		ID:             m.ID,
		ZoneID:         m.ZoneID,
		NodeCode:       m.NodeCode,
		Name:           m.Name,
		Status:         m.Status,
		CPUCoresTotal:  m.CPUCoresTotal,
		CPUCoresUsed:   m.CPUCoresUsed,
		RAMMBTotal:     m.RAMMBTotal,
		RAMMBUsed:      m.RAMMBUsed,
		StorageGBTotal: m.StorageGBTotal,
		StorageGBUsed:  m.StorageGBUsed,
		LastActiveAt:   m.LastActiveAt,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}
