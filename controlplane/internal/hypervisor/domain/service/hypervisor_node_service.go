package hypervisorSvcInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"

	"github.com/google/uuid"
)

// NodeService định nghĩa các nghiệp vụ logic (Business Logic Contract) cho hypervisor nodes.
type NodeService interface {
	// ListNodesByZone thực thi nghiệp vụ lấy danh sách nodes giám sát và dung lượng tải của một Zone.
	ListNodesByZone(ctx context.Context, zoneID uuid.UUID) ([]*hypervisorEntity.HypervisorNode, error)
}
