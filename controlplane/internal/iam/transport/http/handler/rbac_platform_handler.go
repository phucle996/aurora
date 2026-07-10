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

// [COMMENT]: RbacPlatformHandler xử lý các HTTP endpoints quản trị vai trò ở cấp độ platform
type RbacPlatformHandler struct {
	rbacPlatformSvc iamSvcInterface.RbacPlatformService
}

// [COMMENT]: NewRbacPlatformHandler khởi tạo HTTP handler cho Platform RBAC
func NewRbacPlatformHandler(rbacPlatformSvc iamSvcInterface.RbacPlatformService) *RbacPlatformHandler {
	return &RbacPlatformHandler{rbacPlatformSvc: rbacPlatformSvc}
}

// [COMMENT]: AssignUserRole gán role hệ thống cho user (skeleton)
func (h *RbacPlatformHandler) AssignUserRole(c *gin.Context) {
	// [COMMENT]: Sẽ hiện thực hóa ở phase tiếp theo
	apires.RespondSuccess(c, gin.H{"message": "skeleton platform user role assign"}, "success")
}

// [COMMENT]: AssignTenantRole gán role hệ thống cho tenant (skeleton)
func (h *RbacPlatformHandler) AssignTenantRole(c *gin.Context) {
	// [COMMENT]: Sẽ hiện thực hóa ở phase tiếp theo
	apires.RespondSuccess(c, gin.H{"message": "skeleton platform tenant role assign"}, "success")
}

// [COMMENT]: ListRolesPlatform trả về danh sách platform-scoped roles
func (h *RbacPlatformHandler) ListRolesPlatform(c *gin.Context) {
	const op = "iam.rbac.list_platform_roles"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	roles, err := h.rbacPlatformSvc.ListPlatformRoles(ctx)
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

// [COMMENT]: CreateRole tạo vai trò hệ thống mới và map permissions
func (h *RbacPlatformHandler) CreateRole(c *gin.Context) {
	const op = "iam.rbac.create_role"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	var req iamReq.CreateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "invalid create role request schema")
		apires.RespondBadRequest(c, "invalid_request_schema")
		return
	}

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

	err := h.rbacPlatformSvc.CreateRole(ctx, role, permUUIDs)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to create role")
		return
	}

	apires.RespondCreated(c, nil, "role created successfully")
}

// [COMMENT]: GetUserRolesPlatform trả về thông tin vai trò của user hệ thống mục tiêu
func (h *RbacPlatformHandler) GetUserRolesPlatform(c *gin.Context) {
	const op = "iam.rbac.get_user_roles_platform"

	targetUserIDStr := c.Param("id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid user id format")
		return
	}

	callerLevel, ok := constant.GetUserLevel(c, op)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	role, err := h.rbacPlatformSvc.GetUserRoleDetails(ctx, targetUserID, int32(callerLevel))
	if err != nil {
		logger.HandlerWarn(c, op, err, "failed to load role details for target user")
		apires.RespondForbidden(c, "insufficient level hierarchy or role not found")
		return
	}

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

// [COMMENT]: GetRenderContext trả về cấu hình Navigation và Capabilities cho console UI dựa theo user id
func (h *RbacPlatformHandler) GetRenderContext(c *gin.Context) {
	const op = "iam.rbac.get_render_context"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := constant.GetUserID(c, op)
	if !ok {
		return
	}

	renderContext, err := h.rbacPlatformSvc.GetRenderContext(ctx, userID)
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

	// [COMMENT]: Trả về thông tin render context kèm cờ is_personal để frontend xác định giao diện personal/tenant
	apires.RespondSuccess(c, gin.H{
		"navigation":   navDTOs,
		"capabilities": renderContext.Capabilities,
		"is_personal":  renderContext.IsPersonal,
	}, "success")
}

// [COMMENT]: ListPermissions trả về danh sách tất cả các permissions catalog có trong hệ thống
func (h *RbacPlatformHandler) ListPermissions(c *gin.Context) {
	const op = "iam.rbac.list_permissions"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	perms, err := h.rbacPlatformSvc.ListPermissions(ctx)
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
