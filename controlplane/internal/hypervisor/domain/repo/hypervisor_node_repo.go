package hypervisorRepoInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"

	"github.com/google/uuid"
)

// NodeRepository định nghĩa hiệp đồng dữ liệu (Data Contract) cho việc thao tác CSDL Hypervisor Node.
type NodeRepository interface {
	// ListNodesByZone thực hiện truy vấn toàn bộ Hypervisor Nodes thuộc về một Zone cụ thể.
	ListNodesByZone(ctx context.Context, zoneID uuid.UUID) ([]*hypervisorEntity.HypervisorNode, error)
}
