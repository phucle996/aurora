// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/transport/http/handler/tenant_handler.go
//            HTTP Handler cho luồng quản lý Tenant
// ======================================================================================================

package handler

import (
	"context"
	"errors"
	"strings"
	"time"

	entity "controlplane/internal/hierarchy/domain/entity"
	hierarchyservice "controlplane/internal/hierarchy/domain/service"
	taxonomy "controlplane/internal/hierarchy/taxonomy"
	requestdto "controlplane/internal/hierarchy/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: TenantHandler xử lý HTTP requests liên quan đến Tenant
type TenantHandler struct {
	tenantSvc hierarchyservice.TenantService
}

// [COMMENT]: NewTenantHandler tạo instance handler mới với tenant service dependency
func NewTenantHandler(tenantSvc hierarchyservice.TenantService) *TenantHandler {
	return &TenantHandler{
		tenantSvc: tenantSvc,
	}
}

// CreateTenant godoc
// @Summary      Tạo tenant mới
// @Description  Tạo tenant mới và tự động gán user làm admin sáng lập. Không cho phép tạo trong context tenant cũ.
// @Tags         tenants
// @Accept       json
// @Produce      json
// @Param        X-User-ID   header string true  "User ID (UUID) bắt buộc"
// @Param        X-Tenant-ID header string false "Tenant ID (phải trống)"
// @Param        request     body   requestdto.CreateTenantRequest true "Tenant creation body"
// @Success      201 {object} map[string]interface{} "Tenant created"
// @Failure      400 {object} map[string]interface{} "Invalid request / Tenant context already exists"
// @Failure      409 {object} map[string]interface{} "Tenant code already exists"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /api/v1/tenants [post]
func (h *TenantHandler) CreateTenant(c *gin.Context) {
	const op = "hierarchy.tenant.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Ràng buộc: không được phép tạo Tenant bên trong một Tenant context cũ
	tenantIDStr := strings.TrimSpace(c.GetHeader("x-tenant-id"))
	if tenantIDStr != "" {
		logger.HandlerWarn(c, op, nil, "blocked tenant creation: request already under tenant context")
		apires.RespondBadRequest(c, "Invalid request: cannot create a tenant within another tenant")
		return
	}

	// [COMMENT]: Parse header x-user-id — do Edge Proxy inject làm owner_id
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

	// [COMMENT]: Bind JSON body
	var request requestdto.CreateTenantRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind create tenant request failed")
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	// [COMMENT]: Gọi service layer tạo tenant
	tenant, err := h.tenantSvc.CreateTenant(ctx, entity.Tenant{
		Name: strings.TrimSpace(request.Name),
		Code: strings.ToLower(strings.TrimSpace(request.Code)),
	}, ownerID)
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrTenantInvalidInput):
			logger.HandlerWarn(c, op, err, "create tenant invalid input")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, taxonomy.ErrCodeAlreadyExists):
			logger.HandlerWarn(c, op, err, "create tenant code conflict")
			apires.RespondConflict(c, "tenant code already exists")
		case errors.Is(err, taxonomy.ErrTenantInsertFailed):
			logger.HandlerWarn(c, op, err, "create tenant insertion failed")
			apires.RespondBadRequest(c, "tenant creation failed")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	// [COMMENT]: Trả về kết quả thành công
	apires.RespondCreated(c, gin.H{
		"id":         tenant.ID,
		"code":       tenant.Code,
		"name":       tenant.Name,
		"status":     tenant.Status,
		"created_at": tenant.CreatedAt,
	}, "tenant created")
}
