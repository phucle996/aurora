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

// [COMMENT]: WorkspaceTenantHandler chịu trách nhiệm xử lý các luồng HTTP của workspace ở phạm vi doanh nghiệp (Tenant)
type WorkspaceTenantHandler struct {
	tenantSvc coreSvcInterface.TenantWorkspaceService
}

// [COMMENT]: NewWorkspaceTenantHandler tạo một thực thể WorkspaceTenantHandler mới
func NewWorkspaceTenantHandler(
	tenantSvc coreSvcInterface.TenantWorkspaceService,
) *WorkspaceTenantHandler {
	return &WorkspaceTenantHandler{
		tenantSvc: tenantSvc,
	}
}

// CreateWorkspaceTenant godoc
// @Summary      Tạo workspace thuộc tenant
// @Description  Tạo một workspace mới thuộc về doanh nghiệp trong một Zone xác định
// @Tags         workspaces-tenant
// @Accept       json
// @Produce      json
// @Param        X-Zone-ID   header string true "Zone ID (UUID) bắt buộc"
// @Param        X-Tenant-ID header string true "Tenant ID (UUID) bắt buộc"
// @Param        request     body   requestdto.CreateWorkspaceRequest true "Thông tin khởi tạo workspace"
// @Success      201 {object} map[string]interface{} "Workspace created"
// @Router       /api/v1/tenant/hierarchy/workspaces [post]
func (h *WorkspaceTenantHandler) CreateWorkspaceTenant(c *gin.Context) {
	const op = "WorkspaceTenantHandler.CreateWorkspaceTenant"
	// [COMMENT]: Thiết lập context với timeout và định danh operation
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất và kiểm tra định danh Zone ID từ header thông qua helper
	zoneID, ok := constant.GetZoneID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh Tenant ID từ header thông qua helper
	tenantID, ok := constant.GetTenantID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	ownerID, ok := constant.GetUserID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Thực hiện bind JSON request body — dùng DTO riêng cho tenant scope, không lẫn với personal
	var request requestdto.CreateTenantWorkspaceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind create workspace request failed")
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	workspaceEntity := coreEntity.TenantWorkspace{
		Name:        strings.TrimSpace(request.Name),
		Code:        strings.ToLower(strings.TrimSpace(request.Code)),
		Description: request.Description,
		ZoneID:      zoneID,
		TenantID:    tenantID,
		OwnerID:     ownerID,
	}

	// [COMMENT]: Gọi tầng service để xử lý nghiệp vụ tạo workspace thuộc tenant
	workspace, err := h.tenantSvc.CreateWorkspaceForTenant(ctx, workspaceEntity)
	if err != nil {
		switch {
		case errors.Is(err, coreTaxonomy.ErrZoneNotFound):
			logger.HandlerWarn(c, op, err, "create workspace zone not found or not active")
			apires.RespondNotFound(c, "zone not found or not active")
		case errors.Is(err, coreTaxonomy.ErrTenantNotFound):
			logger.HandlerWarn(c, op, err, "create workspace tenant not found or not active")
			apires.RespondNotFound(c, "tenant not found or not active")
		case errors.Is(err, coreTaxonomy.ErrWorkspaceCodeAlreadyExists):
			logger.HandlerWarn(c, op, err, "create workspace code conflict")
			apires.RespondConflict(c, "workspace code already exists within this scope")
		case errors.Is(err, coreTaxonomy.ErrNoRowAffected), errors.Is(err, coreTaxonomy.ErrWorkspaceInsertFailed):
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
		"id":          workspace.ID,
		"name":        workspace.Name,
		"code":        workspace.Code,
		"description": workspace.Description,
		"zone_id":     workspace.ZoneID,
		"tenant_id":   workspace.TenantID,
		"owner_id":    workspace.OwnerID,
		"created_at":  workspace.CreatedAt,
	}, "workspace created")
}

// ListWorkspacesTenant godoc
// @Summary      Lấy danh sách các workspace thuộc tenant
// @Description  Trả về danh sách workspace thuộc tenant mà người dùng được cấp quyền truy cập
// @Tags         workspaces-tenant
// @Accept       json
// @Produce      json
// @Param        X-Tenant-ID   header string true "Tenant ID (UUID) bắt buộc"
// @Param        X-User-Role-ID header string true "Active Role ID (UUID) bắt buộc"
// @Success      200 {object} map[string]interface{} "List workspaces success"
// @Router       /api/v1/tenant/hierarchy/workspaces [get]
func (h *WorkspaceTenantHandler) ListWorkspacesTenant(c *gin.Context) {
	const op = "WorkspaceTenantHandler.ListWorkspacesTenant"
	// [COMMENT]: Thiết lập context với timeout và định danh operation
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	userID, ok := constant.GetUserID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh Tenant ID từ header thông qua helper
	tenantID, ok := constant.GetTenantID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh Active Role ID từ header thông qua helper
	roleID, ok := constant.GetUserRoleID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Truy vấn danh sách các workspace của tenant được phân quyền truy cập
	workspaces, err := h.tenantSvc.ListWorkspacesForTenant(ctx, tenantID, userID, roleID)
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
			"zone_id":     w.ZoneID,
			"tenant_id":   w.TenantID,
			"owner_id":    w.OwnerID,
			"created_at":  w.CreatedAt,
		})
	}

	apires.RespondSuccess(c, data, "list workspaces success")
}

// GetWorkspaceCatalogTenant godoc
// @Summary      Hot path lấy catalog workspace của tenant
// @Description  Trả về danh sách định danh tối giản (id, code, name) của các workspace doanh nghiệp lọc theo zone + quyền của user
// @Tags         workspaces-tenant
// @Accept       json
// @Produce      json
// @Param        X-Zone-ID      header string true "Zone ID (UUID) để lọc catalog"
// @Param        X-Tenant-ID    header string true "Tenant ID (UUID) bắt buộc"
// @Param        X-User-Role-ID  header string true "Active Role ID (UUID) bắt buộc"
// @Success      200 {array}  coreEntity.WorkspaceCatalog "Workspace catalog success"
// @Router       /api/v1/tenant/hierarchy/workspaces/catalog [get]
func (h *WorkspaceTenantHandler) GetWorkspaceCatalogTenant(c *gin.Context) {
	const op = "WorkspaceTenantHandler.GetWorkspaceCatalogTenant"
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

	// [COMMENT]: Trích xuất và kiểm tra định danh Tenant ID từ header thông qua helper
	tenantID, ok := constant.GetTenantID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh Active Role ID từ header thông qua helper
	roleID, ok := constant.GetUserRoleID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Truy vấn danh mục catalog tối giản dựa trên tenant, zone, user và role
	catalog, err := h.tenantSvc.ListWorkspaceCatalogForTenant(ctx, tenantID, zoneID, userID, roleID)
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
