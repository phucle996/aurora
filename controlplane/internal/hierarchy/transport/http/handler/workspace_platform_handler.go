package hierarchyHandler

import (
	"context"
	"errors"
	"strings"
	"time"

	coreEntity "controlplane/internal/hierarchy/domain/entity"
	coreSvcInterface "controlplane/internal/hierarchy/domain/service"
	coreTaxonomy "controlplane/internal/hierarchy/taxonomy"
	requestdto "controlplane/internal/hierarchy/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

// [COMMENT]: WorkspacePlatformHandler chịu trách nhiệm xử lý các luồng HTTP của workspace ở phạm vi cá nhân (Personal/Platform)
type WorkspacePlatformHandler struct {
	personalSvc coreSvcInterface.PersonalWorkspaceService
}

// [COMMENT]: NewWorkspacePlatformHandler tạo một thực thể WorkspacePlatformHandler mới
func NewWorkspacePlatformHandler(
	personalSvc coreSvcInterface.PersonalWorkspaceService,
) *WorkspacePlatformHandler {
	return &WorkspacePlatformHandler{
		personalSvc: personalSvc,
	}
}

// CreateWorkspacePlatform godoc
// @Summary      Tạo workspace cá nhân
// @Description  Tạo một workspace cá nhân mới cho người dùng trong một Zone xác định
// @Tags         workspaces-platform
// @Accept       json
// @Produce      json
// @Param        X-Zone-ID   header string true "Zone ID (UUID) bắt buộc"
// @Param        request     body   requestdto.CreateWorkspaceRequest true "Thông tin khởi tạo workspace"
// @Success      201 {object} map[string]interface{} "Workspace created"
// @Router       /api/v1/platform/hierarchy/workspaces [post]
func (h *WorkspacePlatformHandler) CreateWorkspacePlatform(c *gin.Context) {
	const op = "WorkspacePlatformHandler.CreateWorkspacePlatform"
	// [COMMENT]: Thiết lập context với timeout và định danh operation
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất và kiểm tra định danh Zone ID từ header thông qua helper
	zoneID, ok := constant.GetZoneID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	ownerID, ok := constant.GetUserID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Thực hiện bind JSON request body
	var request requestdto.CreateWorkspaceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind create workspace request failed")
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	workspaceEntity := coreEntity.Workspace{
		Name:     strings.TrimSpace(request.Name),
		Code:     strings.ToLower(strings.TrimSpace(request.Code)),
		ZoneID:   zoneID,
		TenantID: nil, // [COMMENT]: Luôn luôn nil đối với personal/platform scope
		OwnerID:  ownerID,
	}

	// [COMMENT]: Gọi tầng service để xử lý nghiệp vụ tạo workspace cá nhân
	workspace, err := h.personalSvc.CreateWorkspaceForPersonal(ctx, workspaceEntity)
	if err != nil {
		switch {
		case errors.Is(err, coreTaxonomy.ErrZoneNotFound):
			logger.HandlerWarn(c, op, err, "create workspace zone not found or not active")
			apires.RespondNotFound(c, "zone not found or not active")
		case errors.Is(err, coreTaxonomy.ErrWorkspaceCodeAlreadyExists):
			logger.HandlerWarn(c, op, err, "create workspace code conflict")
			apires.RespondConflict(c, "workspace code already exists within this scope")
		case errors.Is(err, coreTaxonomy.ErrNoRowAffected):
			logger.HandlerWarn(c, op, err, "create workspace constraint violation")
			apires.RespondBadRequest(c, "workspace creation failed")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	// [COMMENT]: Trả về kết quả tạo mới thành công
	apires.RespondCreated(c, gin.H{
		"id":         workspace.ID,
		"name":       workspace.Name,
		"code":       workspace.Code,
		"status":     workspace.Status,
		"zone_id":    workspace.ZoneID,
		"tenant_id":  workspace.TenantID,
		"owner_id":   workspace.OwnerID,
		"created_at": workspace.CreatedAt,
	}, "workspace created")
}

// ListWorkspacesPlatform godoc
// @Summary      Lấy danh sách các workspace cá nhân
// @Description  Trả về toàn bộ danh sách workspace cá nhân thuộc sở hữu của người dùng hiện tại
// @Tags         workspaces-platform
// @Accept       json
// @Produce      json
// @Success      200 {object} map[string]interface{} "List workspaces success"
// @Router       /api/v1/platform/hierarchy/workspaces [get]
func (h *WorkspacePlatformHandler) ListWorkspacesPlatform(c *gin.Context) {
	const op = "WorkspacePlatformHandler.ListWorkspacesPlatform"
	// [COMMENT]: Thiết lập context với timeout và định danh operation
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	userID, ok := constant.GetUserID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Truy vấn danh sách các personal workspace thông qua service
	workspaces, err := h.personalSvc.ListWorkspacesForPersonal(ctx, userID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	// [COMMENT]: Format cấu trúc payload phản hồi tối giản
	var data []gin.H
	for _, w := range workspaces {
		data = append(data, gin.H{
			"id":         w.ID,
			"name":       w.Name,
			"code":       w.Code,
			"status":     w.Status,
			"zone_id":    w.ZoneID,
			"tenant_id":  w.TenantID,
			"owner_id":   w.OwnerID,
			"created_at": w.CreatedAt,
		})
	}

	apires.RespondSuccess(c, data, "list workspaces success")
}

// GetWorkspaceCatalogPlatform godoc
// @Summary      Hot path lấy catalog workspace cá nhân
// @Description  Trả về danh sách định danh tối giản (id, code, name) của các workspace cá nhân lọc theo zone
// @Tags         workspaces-platform
// @Accept       json
// @Produce      json
// @Param        X-Zone-ID   header string true "Zone ID (UUID) để lọc catalog"
// @Success      200 {array}  coreEntity.WorkspaceCatalog "Workspace catalog success"
// @Router       /api/v1/platform/hierarchy/workspaces/catalog [get]
func (h *WorkspacePlatformHandler) GetWorkspaceCatalogPlatform(c *gin.Context) {
	const op = "WorkspacePlatformHandler.GetWorkspaceCatalogPlatform"
	// [COMMENT]: Thiết lập context với timeout và định danh operation
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	userID, ok := constant.GetUserID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh Zone ID từ header thông qua helper
	zoneID, ok := constant.GetZoneID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Gọi hot path truy vấn dữ liệu catalog tối giản thông qua service cá nhân
	catalog, err := h.personalSvc.ListWorkspaceCatalogForPersonal(ctx, userID, zoneID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	// [COMMENT]: Trả về kết quả danh mục workspace cá nhân thành công
	apires.RespondSuccess(c, catalog, "workspace catalog success")
}
