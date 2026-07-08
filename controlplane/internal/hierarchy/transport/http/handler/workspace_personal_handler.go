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

// [COMMENT]: WorkspacePersonalHandler chịu trách nhiệm xử lý các luồng HTTP của workspace ở phạm vi cá nhân (Personal/Me)
type WorkspacePersonalHandler struct {
	personalSvc coreSvcInterface.PersonalWorkspaceService
}

// [COMMENT]: NewWorkspacePersonalHandler tạo một thực thể WorkspacePersonalHandler mới
func NewWorkspacePersonalHandler(
	personalSvc coreSvcInterface.PersonalWorkspaceService,
) *WorkspacePersonalHandler {
	return &WorkspacePersonalHandler{
		personalSvc: personalSvc,
	}
}

// CreateWorkspacePersonal godoc
// @Summary      Tạo workspace cá nhân
// @Description  Tạo một workspace cá nhân mới cho người dùng trong một Zone xác định
// @Tags         workspaces-personal
// @Accept       json
// @Produce      json
// @Param        X-Zone-ID   header string true "Zone ID (UUID) bắt buộc"
// @Param        request     body   requestdto.CreateWorkspaceRequest true "Thông tin khởi tạo workspace"
// @Success      201 {object} map[string]interface{} "Workspace created"
// @Router       /api/v1/me/hierarchy/workspaces [post]
func (h *WorkspacePersonalHandler) CreateWorkspacePersonal(c *gin.Context) {
	const op = "WorkspacePersonalHandler.CreateWorkspacePersonal"
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

	// [COMMENT]: Thực hiện bind JSON request body — dùng DTO riêng cho personal scope, không lẫn với tenant
	var request requestdto.CreatePersonalWorkspaceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind create workspace request failed")
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	workspaceEntity := coreEntity.PersonalWorkspace{
		Name:        strings.TrimSpace(request.Name),
		Code:        strings.ToLower(strings.TrimSpace(request.Code)),
		Description: strings.TrimSpace(request.Description),
		ZoneID:      zoneID,
		OwnerID:     ownerID,
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
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"id":          workspace.ID,
		"name":        workspace.Name,
		"code":        workspace.Code,
		"description": workspace.Description,
		"owner_id":    workspace.OwnerID,
		"created_at":  workspace.CreatedAt,
	}, "workspace created")
}

// ListWorkspacesPersonal godoc
// @Summary      Lấy danh sách các workspace cá nhân
// @Description  Trả về toàn bộ danh sách workspace cá nhân thuộc sở hữu của người dùng hiện tại
// @Tags         workspaces-personal
// @Accept       json
// @Produce      json
// @Success      200 {object} map[string]interface{} "List workspaces success"
// @Router       /api/v1/me/hierarchy/workspaces [get]
func (h *WorkspacePersonalHandler) ListWorkspacesPersonal(c *gin.Context) {
	const op = "WorkspacePersonalHandler.ListWorkspacesPersonal"
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
			"id":          w.ID,
			"name":        w.Name,
			"code":        w.Code,
			"description": w.Description,
			"created_at":  w.CreatedAt,
		})
	}

	apires.RespondSuccess(c, data, "list workspaces success")
}

// GetWorkspaceCatalogPersonal godoc
// @Summary      Hot path lấy catalog workspace cá nhân
// @Description  Trả về danh sách định danh tối giản (id, code, name) của các workspace cá nhân lọc theo zone
// @Tags         workspaces-personal
// @Accept       json
// @Produce      json
// @Param        X-Zone-ID   header string true "Zone ID (UUID) để lọc catalog"
// @Success      200 {array}  coreEntity.WorkspaceCatalog "Workspace catalog success"
// @Router       /api/v1/me/hierarchy/workspaces/catalog [get]
func (h *WorkspacePersonalHandler) GetWorkspaceCatalogPersonal(c *gin.Context) {
	const op = "WorkspacePersonalHandler.GetWorkspaceCatalogPersonal"
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

	// [COMMENT]: Format cấu trúc payload catalog tối giản tại layer handler
	var data []gin.H
	for _, ws := range catalog {
		data = append(data, gin.H{
			"id":   ws.ID,
			"code": ws.Code,
			"name": ws.Name,
		})
	}

	apires.RespondSuccess(c, data, "workspace catalog success")
}
