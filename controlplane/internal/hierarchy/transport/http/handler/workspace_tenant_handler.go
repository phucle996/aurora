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
)

// [COMMENT]: WorkspaceTenantHandler chịu trách nhiệm xử lý các luồng HTTP của workspace ở phạm vi doanh nghiệp (Tenant)
type WorkspaceTenantHandler struct {
	tenantSvc hierarchySvcInterface.TenantWorkspaceService
}

// [COMMENT]: NewWorkspaceTenantHandler tạo một thực thể WorkspaceTenantHandler mới
func NewWorkspaceTenantHandler(
	tenantSvc hierarchySvcInterface.TenantWorkspaceService,
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
// @Param        request     body   hierarchyReq.CreateTenantWorkspaceRequest true "Thông tin khởi tạo workspace"
// @Success      201 {object} map[string]interface{} "Workspace created"
// @Router       /api/v1/tenant/hierarchy/workspaces [post]
func (h *WorkspaceTenantHandler) CreateWorkspaceTenant(c *gin.Context) {
	const op = "hierarchy.workspace.tenant.create"
	// [COMMENT]: Thiết lập context với timeout và định danh operation
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất và kiểm tra định danh Zone ID từ header thông qua helper
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh Tenant ID từ header thông qua helper
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	ownerID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Thực hiện bind JSON request body — dùng DTO riêng cho tenant scope, không lẫn với personal
	var request hierarchyReq.CreateTenantWorkspaceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind create workspace request failed")
		apires.RespondBadRequest(c, "invalid request body")
		return
	}
	workspaceName := strings.TrimSpace(request.Name)
	workspaceCode := strings.ToLower(strings.TrimSpace(request.Code))
	if workspaceName == "" || workspaceCode == "" || len(workspaceName) > 255 || len(workspaceCode) > 100 {
		logger.HandlerWarn(c, op, nil, "create tenant workspace normalized input is invalid")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	workspaceEntity := &hierarchyEntity.CreateTenantWorkspace{
		Name:        workspaceName,
		Code:        workspaceCode,
		Description: request.Description,
		ZoneID:      zoneID,
		TenantID:    tenantID,
		OwnerID:     ownerID,
	}

	// [COMMENT]: Gọi tầng service để xử lý nghiệp vụ tạo workspace thuộc tenant
	workspace, err := h.tenantSvc.CreateWorkspaceForTenant(ctx, workspaceEntity)
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
// @Success      200 {object} map[string]interface{} "List workspaces success"
// @Router       /api/v1/tenant/hierarchy/workspaces [get]
func (h *WorkspaceTenantHandler) ListWorkspacesTenant(c *gin.Context) {
	const op = "hierarchy.workspace.tenant.list"
	// [COMMENT]: Thiết lập context với timeout và định danh operation
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	actorUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh Tenant ID từ header thông qua helper
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Truy vấn danh sách các workspace của tenant được phân quyền truy cập
	workspaces, err := h.tenantSvc.ListWorkspacesForTenant(ctx, &hierarchyEntity.ListTenantWorkspaces{
		TenantID: tenantID, ActorUserID: actorUserID,
	})
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
// @Success      200 {object} map[string]interface{} "Workspace catalog success"
// @Router       /api/v1/tenant/hierarchy/workspaces/catalog [get]
func (h *WorkspaceTenantHandler) GetWorkspaceCatalogTenant(c *gin.Context) {
	const op = "hierarchy.workspace.tenant.catalog"
	// [COMMENT]: Thiết lập context với timeout và định danh operation
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất và kiểm tra định danh User ID từ header thông qua helper
	actorUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh Zone ID từ header thông qua helper
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh Tenant ID từ header thông qua helper
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Truy vấn danh mục catalog tối giản dựa trên tenant, zone, user và role
	catalog, err := h.tenantSvc.ListWorkspaceCatalogForTenant(ctx, &hierarchyEntity.ListTenantWorkspaceCatalog{
		TenantID: tenantID, ZoneID: zoneID, ActorUserID: actorUserID,
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

// DeleteWorkspaceTenant godoc
// @Summary      Xóa workspace doanh nghiệp
// @Description  Thực hiện xóa workspace doanh nghiệp nếu không còn bất kỳ tài nguyên nào bên trong
// @Tags         workspaces-tenant
// @Accept       json
// @Produce      json
// @Success      200 {object} map[string]interface{} "Workspace deleted successfully"
// @Router       /api/v1/tenant/hierarchy/workspaces [delete]
func (h *WorkspaceTenantHandler) DeleteWorkspaceTenant(c *gin.Context) {
	const op = "hierarchy.workspace.tenant.delete"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// The ACR-selected workspace is both the authorization subject and the
	// deletion target. This prevents a same-Tenant cross-workspace delete.
	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất và kiểm tra định danh Tenant ID từ header thông qua helper
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	err := h.tenantSvc.DeleteWorkspaceForTenant(ctx, &hierarchyEntity.DeleteTenantWorkspace{ID: workspaceID, TenantID: tenantID})
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
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, nil, "workspace deleted successfully")
}
