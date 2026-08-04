package hierarchyHandler

import (
	"context"
	"errors"
	"strings"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	hierarchyReq "controlplane/internal/hierarchy/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: WorkspacePersonalHandler chịu trách nhiệm xử lý các luồng HTTP của workspace ở phạm vi cá nhân (Personal/Me)
type WorkspacePersonalHandler struct {
	personalSvc hierarchySvcInterface.PersonalWorkspaceService
}

// [COMMENT]: NewWorkspacePersonalHandler tạo một thực thể WorkspacePersonalHandler mới
func NewWorkspacePersonalHandler(
	personalSvc hierarchySvcInterface.PersonalWorkspaceService,
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
// @Param        request     body   hierarchyReq.CreatePersonalWorkspaceRequest true "Thông tin khởi tạo workspace"
// @Success      201 {object} map[string]interface{} "Workspace created"
// @Router       /api/v1/personal/hierarchy/workspaces [post]
func (h *WorkspacePersonalHandler) CreateWorkspacePersonal(c *gin.Context) {
	const op = "hierarchy.workspace.personal.create"
	// [COMMENT]: Thiết lập context với timeout và định danh operation
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất và kiểm tra định danh Zone ID từ header thông qua helper
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	ownerID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Thực hiện bind JSON request body — dùng DTO riêng cho personal scope, không lẫn với tenant
	var request hierarchyReq.CreatePersonalWorkspaceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind create workspace request failed")
		apires.RespondBadRequest(c, "invalid request body")
		return
	}
	workspaceName := strings.TrimSpace(request.Name)
	workspaceCode := strings.ToLower(strings.TrimSpace(request.Code))
	if workspaceName == "" || workspaceCode == "" || len(workspaceName) > 255 || len(workspaceCode) > 100 {
		logger.HandlerWarn(c, op, nil, "create personal workspace normalized input is invalid")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	workspaceEntity := &hierarchyEntity.CreatePersonalWorkspace{
		Name:        workspaceName,
		Code:        workspaceCode,
		Description: strings.TrimSpace(request.Description),
		ZoneID:      zoneID,
		OwnerID:     ownerID,
	}

	// [COMMENT]: Gọi tầng service để xử lý nghiệp vụ tạo workspace cá nhân
	workspace, err := h.personalSvc.CreateWorkspaceForPersonal(ctx, workspaceEntity)
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			logger.HandlerWarn(c, op, err, "create workspace parent not found")
			apires.RespondNotFound(c, "resource not found")
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			logger.HandlerWarn(c, op, err, "create workspace parent precondition failed")
			apires.RespondConflict(c, "resource precondition failed")
		case errors.Is(err, hierarchyTaxonomy.ErrAlreadyExists):
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
// @Router       /api/v1/personal/hierarchy/workspaces [get]
func (h *WorkspacePersonalHandler) ListWorkspacesPersonal(c *gin.Context) {
	const op = "hierarchy.workspace.personal.list"
	// [COMMENT]: Thiết lập context với timeout và định danh operation
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	// [COMMENT]: Truy vấn danh sách các personal workspace thông qua service
	workspaces, err := h.personalSvc.ListWorkspacesForPersonal(ctx, &hierarchyEntity.ListPersonalWorkspaces{OwnerID: userID})
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
// @Success      200 {object} map[string]interface{} "Workspace catalog success"
// @Router       /api/v1/personal/hierarchy/workspaces/catalog [get]
func (h *WorkspacePersonalHandler) GetWorkspaceCatalogPersonal(c *gin.Context) {
	const op = "hierarchy.workspace.personal.catalog"
	// [COMMENT]: Thiết lập context với timeout và định danh operation
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh Zone ID từ header thông qua helper
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Gọi hot path truy vấn dữ liệu catalog tối giản thông qua service cá nhân
	catalog, err := h.personalSvc.ListWorkspaceCatalogForPersonal(ctx, &hierarchyEntity.ListPersonalWorkspaceCatalog{
		OwnerID: userID, ZoneID: zoneID,
	})
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

// DeleteWorkspacePersonal godoc
// @Summary      Xóa workspace cá nhân
// @Description  Thực hiện xóa workspace cá nhân nếu không còn bất kỳ tài nguyên nào bên trong
// @Tags         workspaces-personal
// @Accept       json
// @Produce      json
// @Param        workspace_id   path   string true "Workspace ID (UUID) cần xóa"
// @Success      200 {object} map[string]interface{} "Workspace deleted successfully"
// @Router       /api/v1/personal/hierarchy/workspaces/{workspace_id} [delete]
func (h *WorkspacePersonalHandler) DeleteWorkspacePersonal(c *gin.Context) {
	const op = "hierarchy.workspace.personal.delete"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	workspaceIDStr := c.Param("workspace_id")
	workspaceID, err := uuid.Parse(workspaceIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid workspace_id param format")
		apires.RespondBadRequest(c, "invalid workspace_id format")
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	ownerUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	err = h.personalSvc.DeleteWorkspaceForPersonal(ctx, &hierarchyEntity.DeletePersonalWorkspace{ID: workspaceID, OwnerID: ownerUserID})
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			logger.HandlerWarn(c, op, err, "workspace not found to delete")
			apires.RespondNotFound(c, "workspace not found")
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			logger.HandlerWarn(c, op, err, "delete workspace precondition failed")
			apires.RespondConflict(c, "workspace delete precondition failed")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal server error")
		}
		return
	}

	apires.RespondSuccess(c, nil, "workspace deleted successfully")
}
