package hypervisorHandler

import (
	"context"
	"strings"
	"time"

	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type NodeHandler struct {
	svc hypervisorSvcInterface.NodeService
}

// NewNodeHandler khởi tạo đối tượng HTTP handler phục vụ truy cập Hypervisor API
func NewNodeHandler(svc hypervisorSvcInterface.NodeService) *NodeHandler {
	return &NodeHandler{
		svc: svc,
	}
}

// ListNodes tiếp nhận HTTP request, xác thực zone_id cụ thể và trả về danh sách nodes
func (h *NodeHandler) ListNodes(c *gin.Context) {
	const op = "hypervisor.node.list"

	// [COMMENT]: 1. Định nghĩa Context có timeout cụ thể là 5 giây theo chuẩn thiết kế HA
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: 2. Đọc ngữ cảnh zone_id (UUID) từ header HTTP X-Zone-Context do Gateway/Proxy (ACR) phân giải và tiêm vào
	zoneIDStr := strings.TrimSpace(c.GetHeader("X-Zone-Context"))

	if zoneIDStr == "" || zoneIDStr == "global" {
		logger.HandlerWarn(c, op, nil, "zone context is missing or global zone is not allowed in this action")
		apires.RespondBadRequest(c, "zone context is missing or global zone is not allowed")
		return
	}

	// [COMMENT]: 3. Ràng buộc bắt buộc truyền tham số zone_id cụ thể hợp lệ
	zoneID, err := uuid.Parse(zoneIDStr)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondBadRequest(c, "zone is invalid")
		return
	}

	// [COMMENT]: 5. Gọi business service layer xử lý nghiệp vụ với context đã được tiêm operation & timeout
	nodes, err := h.svc.ListNodesByZone(ctx, zoneID)
	if err != nil {
		// [COMMENT]: Thiết kế log flow sử dụng standard logger.HandlerError
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal server error")
		return
	}

	// [COMMENT]: 6. Sử dụng raw gin.H trực tiếp để map dữ liệu từ Domain Entities (không thông qua DTO struct)
	rows := make([]gin.H, 0, len(nodes))
	for _, item := range nodes {
		if item != nil {
			rows = append(rows, gin.H{
				"id":               item.ID,
				"node_code":        item.NodeCode,
				"name":             item.Name,
				"status":           item.Status,
				"cpu_cores_total":  item.CPUCoresTotal,
				"cpu_cores_used":   item.CPUCoresUsed,
				"ram_mb_total":     item.RAMMBTotal,
				"ram_mb_used":      item.RAMMBUsed,
				"storage_gb_total": item.StorageGBTotal,
				"storage_gb_used":  item.StorageGBUsed,
				"last_active_at":   item.LastActiveAt,
				"created_at":       item.CreatedAt,
				"updated_at":       item.UpdatedAt,
			})
		}
	}

	// [COMMENT]: 7. Sử dụng apires helper để định dạng cấu trúc JSON trả về đồng bộ
	apires.RespondSuccess(c, gin.H{
		"nodes": rows,
	}, "hypervisor nodes fetched")
}
