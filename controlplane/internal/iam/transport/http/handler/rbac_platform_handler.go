package iamHandler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamReq "controlplane/internal/iam/transport/http/dto/req"
	"controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
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

// [COMMENT]: AssignUserRole gán role hệ thống cho user
func (h *RbacPlatformHandler) AssignUserRole(c *gin.Context) {
	const op = "iam.rbac.assign_user_role"

	// 1. Trích xuất level của caller từ context headers (X-User-Level)
	callerLevel, ok := pkgcontext.GetUserLevel(c, op)
	if !ok {
		return
	}

	// 2. Bind payload request chứa target user_id và role_id gán từ DTO
	var req iamReq.AssignUserRolePlatformRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "invalid assign user role request payload")
		apires.RespondBadRequest(c, "invalid_request_schema")
		return
	}

	// 3. Phân tích định dạng UUID từ request
	userUUID, err := uuid.Parse(req.UserID)
	if err != nil {
		apires.RespondBadRequest(c, "invalid user id format")
		return
	}
	roleUUID, err := uuid.Parse(req.RoleID)
	if err != nil {
		apires.RespondBadRequest(c, "invalid role id format")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 4. Gọi Service thực thi phân quyền và kiểm tra phân cấp
	err = h.rbacPlatformSvc.AssignUserRole(ctx, callerLevel, userUUID, roleUUID)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrUserNotFound) {
			apires.RespondNotFound(c, "user not found")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			apires.RespondNotFound(c, "role not found")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			apires.RespondForbidden(c, "insufficient level hierarchy or role assign not allowed")
			return
		}

		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to assign user role")
		return
	}

	// 5. Phản hồi thành công gán vai trò platform cho user kèm log nghiệp vụ thành công
	logger.HandlerInfo(c, op, fmt.Sprintf("user role assigned successfully: userID=%s, roleID=%s", req.UserID, req.RoleID))
	apires.RespondSuccess(c, nil, "user role assigned successfully")
}

// [COMMENT]: ListRolesPlatform trả về danh sách platform-scoped roles có level thấp hơn caller
func (h *RbacPlatformHandler) ListRolesPlatform(c *gin.Context) {
	const op = "iam.rbac.list_platform_roles"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Lấy callerLevel từ header X-User-Level do ACR inject để lọc roles có level thấp hơn
	callerLevel, ok := pkgcontext.GetUserLevel(c, op)
	if !ok {
		return
	}

	roles, err := h.rbacPlatformSvc.ListPlatformRoles(ctx, callerLevel)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	resp := make([]gin.H, 0, len(roles))
	for _, r := range roles {
		resp = append(resp, gin.H{
			"id":                r.ID.String(),
			"code":              r.Code,
			"name":              r.Name,
			"description":       r.Description,
			"role_level":        r.RoleLevel,
			"scope":             r.Scope,
			"assignments_count": r.AssignmentsCount,
			"permissions_count": r.PermissionsCount,
			"created_by":        r.CreatedBy.String(),
			"created_by_name":   r.CreatedByName,
			"created_at":        r.CreatedAt.Format(time.RFC3339),
			"updated_at":        r.UpdatedAt.Format(time.RFC3339),
		})
	}

	apires.RespondSuccess(c, gin.H{"roles": resp}, "success")
}

// [COMMENT]: CreateRole tạo vai trò hệ thống mới và map permissions kèm kiểm tra sở hữu tập con quyền của caller
func (h *RbacPlatformHandler) CreateRole(c *gin.Context) {
	const op = "iam.rbac.create_role"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	callerUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

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
		CreatedBy:   callerUserID,
	}

	err := h.rbacPlatformSvc.CreateRole(ctx, callerUserID, role, permUUIDs)
	if err != nil {
		logger.HandlerError(c, op, err)
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			apires.RespondForbidden(c, "insufficient_permission_subset: cannot assign unowned permissions")
			return
		}
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

	callerLevel, ok := pkgcontext.GetUserLevel(c, op)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
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
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
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

// [COMMENT]: ListPermissions trả về danh sách các permissions catalog được lọc theo quyền của caller
func (h *RbacPlatformHandler) ListPermissions(c *gin.Context) {
	const op = "iam.rbac.list_permissions"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	callerUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	perms, err := h.rbacPlatformSvc.ListPermissions(ctx, callerUserID)
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

// [COMMENT]: DeleteRolePlatform xóa vai trò platform hệ thống
func (h *RbacPlatformHandler) DeleteRolePlatform(c *gin.Context) {
	const op = "iam.rbac.delete_role"
	callerLevel, ok := pkgcontext.GetUserLevel(c, op)
	if !ok {
		return
	}

	roleIDStr := c.Param("role_id")
	roleID, err := uuid.Parse(roleIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid role id format")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	err = h.rbacPlatformSvc.DeleteRolePlatform(ctx, callerLevel, roleID)
	if err != nil {
		logger.HandlerError(c, op, err)
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			apires.RespondForbidden(c, "Action not allowed: hierarchical check failed or role is currently assigned to users/tenants")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			apires.RespondNotFound(c, "Role not found")
			return
		}
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	apires.RespondSuccess(c, nil, "Role deleted successfully")
}

// [COMMENT]: GetRoleDetailsPlatform trả về chi tiết vai trò platform cùng danh sách quyền phẳng
func (h *RbacPlatformHandler) GetRoleDetailsPlatform(c *gin.Context) {
	const op = "iam.rbac.get_role_details"

	callerLevel, ok := pkgcontext.GetUserLevel(c, op)
	if !ok {
		return
	}

	roleIDStr := c.Param("role_id")
	roleID, err := uuid.Parse(roleIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid role id format")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	role, permissions, err := h.rbacPlatformSvc.GetRoleDetails(ctx, callerLevel, roleID)
	if err != nil {
		logger.HandlerError(c, op, err)
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			apires.RespondNotFound(c, "Role not found")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			apires.RespondForbidden(c, "Action not allowed: hierarchical check failed")
			return
		}
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	// [COMMENT]: Map danh sách đối tượng permission phẳng sang JSON phẳng
	permsResp := make([]gin.H, 0, len(permissions))
	for _, p := range permissions {
		permsResp = append(permsResp, gin.H{
			"id":          p.ID.String(),
			"module":      p.Module,
			"object":      p.Object,
			"behavior":    p.Behavior,
			"description": p.Description,
		})
	}

	resp := gin.H{
		"id":                role.ID.String(),
		"code":              role.Code,
		"name":              role.Name,
		"description":       role.Description,
		"role_level":        role.RoleLevel,
		"scope":             role.Scope,
		"assignments_count": role.AssignmentsCount,
		"permissions_count": role.PermissionsCount,
		"created_by":        role.CreatedBy.String(),
		"created_by_name":   role.CreatedByName,
		"created_at":        role.CreatedAt.Format(time.RFC3339),
		"updated_at":        role.UpdatedAt.Format(time.RFC3339),
		"permissions":       permsResp,
	}

	apires.RespondSuccess(c, gin.H{"role": resp}, "role details fetched successfully")
}

// [COMMENT]: UpdateRolePlatform thực hiện cập nhật thông tin vai trò platform và đồng bộ danh sách quyền được gán có kiểm tra cấp bậc caller level cùng tập con quyền của caller
func (h *RbacPlatformHandler) UpdateRolePlatform(c *gin.Context) {
	const op = "iam.rbac.update_role"

	callerLevel, ok := pkgcontext.GetUserLevel(c, op)
	if !ok {
		return
	}

	callerUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	roleIDStr := c.Param("role_id")
	roleID, err := uuid.Parse(roleIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid role id format")
		return
	}

	var req iamReq.UpdateRolePlatformReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, err.Error())
		return
	}

	// [COMMENT]: Chuyển đổi mảng string permission_ids sang uuid.UUID
	permIDs := make([]uuid.UUID, len(req.PermissionIDs))
	for i, idStr := range req.PermissionIDs {
		pID, err := uuid.Parse(idStr)
		if err != nil {
			apires.RespondBadRequest(c, fmt.Sprintf("invalid permission id format at index %d", i))
			return
		}
		permIDs[i] = pID
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	input := &iamEntity.UpdateRoleInput{
		ID:            roleID,
		Name:          req.Name,
		Description:   req.Description,
		PermissionIDs: permIDs,
	}

	err = h.rbacPlatformSvc.UpdateRole(ctx, callerUserID, callerLevel, input)
	if err != nil {
		logger.HandlerError(c, op, err)
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			apires.RespondNotFound(c, "Role not found")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			apires.RespondForbidden(c, "Action not allowed: hierarchical check or permission subset check failed")
			return
		}
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	apires.RespondSuccess(c, nil, "Role updated successfully")
}
