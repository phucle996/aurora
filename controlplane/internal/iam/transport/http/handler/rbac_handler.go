package iamHandler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamReq "controlplane/internal/iam/transport/http/dto/req"
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: RbacHandler xử lý các HTTP endpoints cho IAM RBAC (dạng skeleton cho phase tiếp theo)
type RbacHandler struct {
	rbacSvc iamSvcInterface.RbacService
}

// [COMMENT]: NewRbacHandler khởi tạo HTTP handler tối giản cho RBAC
func NewRbacHandler(rbacSvc iamSvcInterface.RbacService) *RbacHandler {
	return &RbacHandler{rbacSvc: rbacSvc}
}

// [COMMENT]: AssignUserRole gán role và permissions cho user (skeleton HTTP handler)
func (h *RbacHandler) AssignUserRole(c *gin.Context) {
	// [COMMENT]: Sẽ được hiện thực hóa ở phase tiếp theo
	apires.RespondSuccess(c, gin.H{"message": "skeleton"}, "success")
}

// [COMMENT]: AssignTenantRole gán role và permissions cho tenant (skeleton HTTP handler)
func (h *RbacHandler) AssignTenantRole(c *gin.Context) {
	// [COMMENT]: Sẽ được hiện thực hóa ở phase tiếp theo
	apires.RespondSuccess(c, gin.H{"message": "skeleton"}, "success")
}

// [COMMENT]: ListRolesPlatform trả về toàn bộ danh sách platform-scoped roles bọc trong gin.H
func (h *RbacHandler) ListRolesPlatform(c *gin.Context) {

	const op = "iam.rbac.list_platform_roles"

	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	roles, err := h.rbacSvc.ListPlatformRoles(ctx)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	resp := make([]gin.H, 0, len(roles))
	for _, r := range roles {
		resp = append(resp, gin.H{
			"id":         r.ID.String(),
			"code":       r.Code,
			"name":       r.Name,
			"role_level": r.RoleLevel,
			"scope":      r.Scope,
		})
	}

	apires.RespondSuccess(c, gin.H{"roles": resp}, "success")
}

// [COMMENT]: ListRolesTenant trả về danh sách roles được gán cho tenant cụ thể lấy từ header X-Tenant-ID
func (h *RbacHandler) ListRolesTenant(c *gin.Context) {

	const op = "iam.rbac.list_tenant_roles"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	tenantID, ok := constant.GetTenantID(c, op)
	if !ok {
		return
	}

	roles, err := h.rbacSvc.ListTenantRoles(ctx, tenantID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	resp := make([]gin.H, 0, len(roles))
	for _, r := range roles {
		resp = append(resp, gin.H{
			"id":         r.ID.String(),
			"code":       r.Code,
			"name":       r.Name,
			"role_level": r.RoleLevel,
			"scope":      r.Scope,
		})
	}

	apires.RespondSuccess(c, gin.H{"roles": resp}, "success")
}

// [COMMENT]: GetRenderContext trả về cấu hình Navigation và Capabilities cho console UI dựa theo user id
func (h *RbacHandler) GetRenderContext(c *gin.Context) {
	const op = "iam.rbac.get_render_context"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := constant.GetUserID(c, op)
	if !ok {
		return
	}

	renderContext, err := h.rbacSvc.GetRenderContext(ctx, userID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	type navItemDTO struct {
		Key     string   `json:"key"`
		Actions []string `json:"actions"`
	}
	navDTOs := make([]navItemDTO, 0, len(renderContext.Navigation))
	for _, nav := range renderContext.Navigation {
		navDTOs = append(navDTOs, navItemDTO{
			Key:     nav.Key,
			Actions: nav.Actions,
		})
	}

	apires.RespondSuccess(c, gin.H{
		"navigation":   navDTOs,
		"capabilities": renderContext.Capabilities,
	}, "success")
}

// [COMMENT]: CreateRole tiếp nhận yêu cầu POST tạo vai trò mới từ console UI
func (h *RbacHandler) CreateRole(c *gin.Context) {
	const op = "iam.rbac.create_role"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	var req iamReq.CreateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "invalid create role request schema")
		apires.RespondBadRequest(c, "invalid_request_schema")
		return
	}

	// [COMMENT]: Chuyển đổi mã role_code sang chữ thường để đồng bộ định dạng mã hóa hệ thống
	codeClean := strings.ToLower(strings.TrimSpace(req.Code))
	if codeClean == "" {
		apires.RespondBadRequest(c, "role code is required")
		return
	}

	permUUIDs := make([]uuid.UUID, 0, len(req.PermissionIDs))
	for _, pIDStr := range req.PermissionIDs {
		pUUID, err := uuid.Parse(pIDStr)
		if err != nil {
			logger.HandlerWarn(c, op, err, "invalid permission uuid format in selection list")
			apires.RespondBadRequest(c, "invalid permission id format")
			return
		}
		permUUIDs = append(permUUIDs, pUUID)
	}

	// [COMMENT]: Kiểm tra phân cấp level (Hierarchy Level). Quyền to hơn = level số nhỏ hơn.
	userLevelStr := strings.TrimSpace(c.GetHeader("X-User-Level"))
	if userLevelStr != "" {
		actorLevel, err := strconv.Atoi(userLevelStr)
		if err == nil {
			if req.RoleLevel < actorLevel {
				logger.HandlerWarn(c, op, nil, fmt.Sprintf("user level hierarchy violation: actorLevel=%d wants to create roleLevel=%d", actorLevel, req.RoleLevel))
				apires.RespondForbidden(c, "insufficient_level_hierarchy")
				return
			}
		}
	}

	role := &iamEntity.Role{
		ID:          uuid.New(),
		Code:        codeClean,
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		RoleLevel:   req.RoleLevel,
		Scope:       req.Scope,
	}

	err := h.rbacSvc.CreateRole(ctx, role, permUUIDs)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to create role")
		return
	}

	apires.RespondCreated(c, nil, "role created successfully")
}

// [COMMENT]: ListPermissions trả về danh sách tất cả các permissions catalog có trong hệ thống
func (h *RbacHandler) ListPermissions(c *gin.Context) {
	const op = "iam.rbac.list_permissions"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	perms, err := h.rbacSvc.ListPermissions(ctx)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to load permissions")
		return
	}

	resp := make([]gin.H, 0, len(perms))
	for _, p := range perms {
		resp = append(resp, gin.H{
			"id":          p.ID.String(),
			"module":      p.Module,
			"object":      p.Object,
			"behavior":    p.Behavior,
			"description": p.Description,
		})
	}

	apires.RespondSuccess(c, gin.H{"permissions": resp}, "permissions fetched successfully")
}

// [COMMENT]: GetUserRolesPlatform trả về thông tin vai trò (Role) của user mục tiêu kèm kiểm tra cấp bậc
func (h *RbacHandler) GetUserRolesPlatform(c *gin.Context) {
	const op = "iam.rbac.get_user_roles_platform"

	// 1. Trích xuất ID của user mục tiêu
	targetUserIDStr := c.Param("id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid user id format")
		return
	}

	// 2. Trích xuất caller level từ header
	callerLevel, ok := constant.GetUserLevel(c, op)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 3. Truy vấn chi tiết vai trò kèm lọc theo level
	role, err := h.rbacSvc.GetUserRoleDetails(ctx, targetUserID, int32(callerLevel))
	if err != nil {
		logger.HandlerWarn(c, op, err, "failed to load role details for target user")
		apires.RespondForbidden(c, "insufficient level hierarchy or role not found")
		return
	}

	// [COMMENT]: Trả về kết quả thành công với key 'role_level' đồng bộ toàn hệ thống
	apires.RespondSuccess(c, gin.H{
		"role": gin.H{
			"id":          role.ID.String(),
			"code":        role.Code,
			"name":        role.Name,
			"description": role.Description,
			"role_level":  role.RoleLevel,
			"scope":       role.Scope,
		},
	}, "user role fetched successfully")
}
