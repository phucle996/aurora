package hypervisorSvcImpl

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"

	"github.com/google/uuid"
)

type NodeServiceImpl struct {
	repo hypervisorRepoInterface.NodeRepository
}

// NewNodeService khởi tạo Business Logic Service cho hypervisor nodes
func NewNodeService(nodeRepo hypervisorRepoInterface.NodeRepository) hypervisorSvcInterface.NodeService {
	return &NodeServiceImpl{
		repo: nodeRepo,
	}
}

// ListNodesByZone thực hiện gọi xuống Repo Layer để lấy danh sách nodes
func (s *NodeServiceImpl) ListNodesByZone(ctx context.Context, zoneID uuid.UUID) ([]*hypervisorEntity.HypervisorNode, error) {
	// [COMMENT]: Thực thi business logic và đối soát nếu cần trước khi gọi DB
	return s.repo.ListNodesByZone(ctx, zoneID)
}
