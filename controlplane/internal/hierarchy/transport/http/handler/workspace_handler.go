// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/transport/http/handler/workspace_handler.go
//            HTTP Handler cho luồng quản lý Workspace
// ======================================================================================================

package zoneHandler

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
	workspaceSvc coreSvcInterface.WorkspaceService
}

// [COMMENT]: NewWorkspaceHandler tạo instance handler mới với workspace service dependency
func NewWorkspaceHandler(workspaceSvc coreSvcInterface.WorkspaceService) *WorkspaceHandler {
	return &WorkspaceHandler{
		workspaceSvc: workspaceSvc,
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

	// [COMMENT]: Bind JSON body (chỉ chứa trường name)
	var request requestdto.CreateWorkspaceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind create workspace request failed")
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	// [COMMENT]: Gọi service layer tạo workspace
	workspace, err := h.workspaceSvc.CreateWorkspace(ctx, coreEntity.CreateWorkspaceInput{
		Name:     strings.TrimSpace(request.Name),
		ZoneID:   zoneID,
		TenantID: tenantID,
		OwnerID:  ownerID,
	})
	if err != nil {
		switch {
		case errors.Is(err, coreTaxonomy.ErrWorkspaceInvalidInput):
			logger.HandlerWarn(c, op, err, "create workspace invalid input")
			apires.RespondBadRequest(c, "Invalid request")
		case errors.Is(err, coreTaxonomy.ErrWorkspaceZoneNotFound):
			logger.HandlerWarn(c, op, err, "create workspace zone not found or not active")
			apires.RespondNotFound(c, "zone not found or not active")
		case errors.Is(err, coreTaxonomy.ErrWorkspaceTenantNotFound):
			logger.HandlerWarn(c, op, err, "create workspace tenant not found or not active")
			apires.RespondNotFound(c, "tenant not found or not active")
		case errors.Is(err, coreTaxonomy.ErrWorkspaceInsertFailed):
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
		"status":     workspace.Status,
		"zone_id":    workspace.ZoneID,
		"tenant_id":  workspace.TenantID,
		"owner_id":   workspace.OwnerID,
		"created_at": workspace.CreatedAt,
	}, "workspace created")
}
