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
	"github.com/google/uuid"
)

// [COMMENT]: WorkspaceHandler xử lý HTTP requests liên quan đến Workspace
type WorkspaceHandler struct {
	tenantSvc   coreSvcInterface.TenantWorkspaceService
	personalSvc coreSvcInterface.PersonalWorkspaceService
}

// [COMMENT]: NewWorkspaceHandler tạo instance handler mới với tenant và personal services
func NewWorkspaceHandler(
	tenantSvc coreSvcInterface.TenantWorkspaceService,
	personalSvc coreSvcInterface.PersonalWorkspaceService,
) *WorkspaceHandler {
	return &WorkspaceHandler{
		tenantSvc:   tenantSvc,
		personalSvc: personalSvc,
	}
}

// CreateWorkspace godoc
// @Summary      Tạo workspace mới
// @Description  Tạo workspace trong zone cụ thể, có thể thuộc tenant hoặc workspace cá nhân
// @Tags         workspaces
// @Accept       json
// @Produce      json
// @Param        X-Zone-ID   header string true  "Zone ID (UUID) bắt buộc"
// @Param        X-Tenant-ID header string false "Tenant ID (UUID), empty nếu workspace cá nhân"
// @Param        request     body   requestdto.CreateWorkspaceRequest true "Workspace creation body"
// @Success      201 {object} map[string]interface{} "Workspace created"
// @Failure      400 {object} map[string]interface{} "Invalid request"
// @Failure      404 {object} map[string]interface{} "Zone or Tenant not found"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /api/v1/workspaces [post]
func (h *WorkspaceHandler) CreateWorkspace(c *gin.Context) {
	const op = "workspace.create"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Parse header X-Zone-ID — bắt buộc, phải là UUID hợp lệ
	zoneIDStr := strings.TrimSpace(c.GetHeader("x-zone-id"))
	if zoneIDStr == "" {
		logger.HandlerWarn(c, op, nil, "missing required x-zone-id header")
		apires.RespondBadRequest(c, "Invalid request")
		return
	}
	zoneID, err := uuid.Parse(zoneIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid x-zone-id header format")
		apires.RespondBadRequest(c, "Invalid request")
		return
	}

	// [COMMENT]: Parse header X-Tenant-ID — optional, empty string = workspace cá nhân (tenant_id = NULL)
	var tenantID *uuid.UUID
	tenantIDStr := strings.TrimSpace(c.GetHeader("x-tenant-id"))
	if tenantIDStr != "" {
		parsed, err := uuid.Parse(tenantIDStr)
		if err != nil {
			logger.HandlerWarn(c, op, err, "invalid x-tenant-id header format")
			apires.RespondBadRequest(c, "Invalid request")
			return
		}
		tenantID = &parsed
	}

	// [COMMENT]: Parse header x-user-id — do Edge Proxy (ACR) inject sau xác thực JWT
	userIDStr := strings.TrimSpace(c.GetHeader("x-user-id"))
	if userIDStr == "" {
		logger.HandlerWarn(c, op, nil, "unauthorized - missing x-user-id header")
		apires.RespondUnauthorized(c, "Invalid request")
		return
	}
	ownerID, err := uuid.Parse(userIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid x-user-id header format")
		apires.RespondBadRequest(c, "Invalid request")
		return
	}

	// [COMMENT]: Bind JSON body (chỉ chứa trường name và code)
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
		TenantID: tenantID,
		OwnerID:  ownerID,
	}

	var workspace *coreEntity.Workspace
	// [COMMENT]: Phân luồng dòng chảy dựa trên context X-Tenant-ID
	if tenantID != nil {
		workspace, err = h.tenantSvc.CreateWorkspaceForTenant(ctx, workspaceEntity)
	} else {
		workspace, err = h.personalSvc.CreateWorkspaceForPersonal(ctx, workspaceEntity)
	}

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
		case errors.Is(err, coreTaxonomy.ErrNoRowAffected):
			logger.HandlerWarn(c, op, err, "create workspace constraint violation")
			apires.RespondBadRequest(c, "workspace creation failed")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	// [COMMENT]: Trả về workspace vừa tạo thành công
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

// ListWorkspaces godoc
// @Summary      Lấy danh sách các workspace của user có quyền read
// @Description  Trả về danh sách workspace cá nhân hoặc workspace thuộc tenant được phân quyền read
// @Tags         workspaces
// @Accept       json
// @Produce      json
// @Param        X-Tenant-ID header string false "Tenant ID (UUID), empty nếu lấy danh sách workspace cá nhân"
// @Success      200 {object} map[string]interface{} "List workspaces success"
// @Failure      400 {object} map[string]interface{} "Invalid request"
// @Failure      401 {object} map[string]interface{} "Unauthorized"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /api/v1/workspaces [get]
func (h *WorkspaceHandler) ListWorkspaces(c *gin.Context) {
	const op = "workspace.list"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Parse header X-User-ID
	userIDStr := strings.TrimSpace(c.GetHeader("x-user-id"))
	if userIDStr == "" {
		logger.HandlerWarn(c, op, nil, "unauthorized - missing x-user-id header")
		apires.RespondUnauthorized(c, "Invalid request")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid x-user-id header format")
		apires.RespondBadRequest(c, "Invalid request")
		return
	}

	// [COMMENT]: Lấy danh sách workspace dựa trên context X-Tenant-ID
	var workspaces []*coreEntity.Workspace
	tenantIDStr := strings.TrimSpace(c.GetHeader("x-tenant-id"))

	if tenantIDStr != "" {
		tenantID, err := uuid.Parse(tenantIDStr)
		if err != nil {
			logger.HandlerWarn(c, op, err, "invalid x-tenant-id header format")
			apires.RespondBadRequest(c, "Invalid request")
			return
		}
		// [COMMENT]: Parse header X-User-Role-ID bắt buộc trong Tenant context để tra cứu cache
		roleIDStr := strings.TrimSpace(c.GetHeader("x-user-role-id"))
		if roleIDStr == "" {
			logger.HandlerWarn(c, op, nil, "missing required x-user-role-id header in tenant context")
			apires.RespondBadRequest(c, "Invalid request")
			return
		}
		roleID, err := uuid.Parse(roleIDStr)
		if err != nil {
			logger.HandlerWarn(c, op, err, "invalid x-user-role-id header format")
			apires.RespondBadRequest(c, "Invalid request")
			return
		}

		// [COMMENT]: Lấy các workspace thuộc Tenant mà user được phân quyền read thông qua L1 cache của role
		workspaces, err = h.tenantSvc.ListWorkspacesForTenant(ctx, tenantID, userID, roleID)
		if err != nil {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
			return
		}
	} else {
		// [COMMENT]: Lấy các workspace cá nhân (Personal Scope) thuộc user
		workspaces, err = h.personalSvc.ListWorkspacesForPersonal(ctx, userID)
		if err != nil {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
			return
		}
	}

	// [COMMENT]: Format và trả về dữ liệu catalog workspaces
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

// [COMMENT]: GetWorkspaceCatalog xử lý hot path catalog — trả về danh sách tối giản (id, code, name) theo zone và tenant/personal context
func (h *WorkspaceHandler) GetWorkspaceCatalog(c *gin.Context) {
	const op = "WorkspaceHandler.GetWorkspaceCatalog"

	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Parse bắt buộc X-User-ID cho cả 2 context
	userIDStr := strings.TrimSpace(c.GetHeader("x-user-id"))
	if userIDStr == "" {
		logger.HandlerWarn(c, op, nil, "missing required x-user-id header")
		apires.RespondBadRequest(c, "Invalid request")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid x-user-id header format")
		apires.RespondBadRequest(c, "Invalid request")
		return
	}

	// [COMMENT]: Parse bắt buộc X-Zone-ID để lọc catalog theo deployment zone hiện tại
	zoneIDStr := strings.TrimSpace(c.GetHeader("x-zone-id"))
	if zoneIDStr == "" {
		logger.HandlerWarn(c, op, nil, "missing required x-zone-id header")
		apires.RespondBadRequest(c, "Invalid request")
		return
	}
	zoneID, err := uuid.Parse(zoneIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid x-zone-id header format")
		apires.RespondBadRequest(c, "Invalid request")
		return
	}

	var catalog []coreEntity.WorkspaceCatalog
	tenantIDStr := strings.TrimSpace(c.GetHeader("x-tenant-id"))

	if tenantIDStr != "" {
		tenantID, err := uuid.Parse(tenantIDStr)
		if err != nil {
			logger.HandlerWarn(c, op, err, "invalid x-tenant-id header format")
			apires.RespondBadRequest(c, "Invalid request")
			return
		}
		// [COMMENT]: Parse X-User-Role-ID bắt buộc trong Tenant context để tra cứu cache permissions
		roleIDStr := strings.TrimSpace(c.GetHeader("x-user-role-id"))
		if roleIDStr == "" {
			logger.HandlerWarn(c, op, nil, "missing required x-user-role-id header in tenant context")
			apires.RespondBadRequest(c, "Invalid request")
			return
		}
		roleID, err := uuid.Parse(roleIDStr)
		if err != nil {
			logger.HandlerWarn(c, op, err, "invalid x-user-role-id header format")
			apires.RespondBadRequest(c, "Invalid request")
			return
		}

		// [COMMENT]: Gọi hot path Tenant catalog — parse cache permission rồi query SELECT id,code,name lọc theo zone
		catalog, err = h.tenantSvc.ListWorkspaceCatalogForTenant(ctx, tenantID, zoneID, userID, roleID)
		if err != nil {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
			return
		}
	} else {
		// [COMMENT]: Gọi hot path Personal catalog — query trực tiếp theo owner_id + zone_id
		catalog, err = h.personalSvc.ListWorkspaceCatalogForPersonal(ctx, userID, zoneID)
		if err != nil {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
			return
		}
	}

	// [COMMENT]: Trả về catalog tối giản — payload nhẹ nhất cho hot path
	apires.RespondSuccess(c, catalog, "workspace catalog success")
}
