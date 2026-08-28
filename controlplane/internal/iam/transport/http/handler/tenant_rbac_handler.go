package iamHandler

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamReq "controlplane/internal/iam/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var tenantRoleCodePattern = regexp.MustCompile(`^[a-z0-9_]{2,100}$`)

type TenantRbacHandler struct {
	tenantRbacSvc iamSvcInterface.TenantRbacService
}

func NewTenantRbacHandler(tenantRbacSvc iamSvcInterface.TenantRbacService) *TenantRbacHandler {
	return &TenantRbacHandler{tenantRbacSvc: tenantRbacSvc}
}

func (h *TenantRbacHandler) ListRolesTenant(c *gin.Context) {
	const op = "iam.rbac.tenant_role.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	actorUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	roles, err := h.tenantRbacSvc.ListTenantRoles(ctx, &iamEntity.ListTenantRoles{
		ActorUserID: actorUserID,
		TenantID:    tenantID,
	})
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			logger.HandlerWarn(c, op, err, "tenant role list denied")
			apires.RespondForbidden(c, "action not allowed")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	data := make([]gin.H, 0, len(roles))
	for _, role := range roles {
		data = append(data, gin.H{
			"id":                         role.ID,
			"code":                       role.Code,
			"name":                       role.Name,
			"description":                role.Description,
			"role_level":                 role.RoleLevel,
			"version":                    role.Version,
			"assignments_count":          role.AssignmentsCount,
			"outdated_assignments_count": role.OutdatedAssignmentsCount,
			"permissions_count":          role.PermissionsCount,
			"created_at":                 role.CreatedAt,
		})
	}
	apires.RespondSuccess(c, gin.H{"roles": data}, "success")
}

func (h *TenantRbacHandler) GetTenantRole(c *gin.Context) {
	const op = "iam.rbac.tenant_role.get"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	actorUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}
	roleID, err := uuid.Parse(strings.TrimSpace(c.Param("role_id")))
	if err != nil || roleID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	role, err := h.tenantRbacSvc.GetTenantRole(ctx, &iamEntity.GetTenantRole{ActorUserID: actorUserID, TenantID: tenantID, ID: roleID})
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "role not found")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			apires.RespondForbidden(c, "action not allowed")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	permissions := make([]gin.H, 0, len(role.Permissions))
	for _, permission := range role.Permissions {
		permissions = append(permissions, gin.H{"id": permission.ID, "module": permission.Module, "object": permission.Object, "behavior": permission.Behavior, "description": permission.Description})
	}
	apires.RespondSuccess(c, gin.H{"role": gin.H{
		"id": role.ID, "code": role.Code, "name": role.Name, "description": role.Description,
		"role_level": role.RoleLevel, "version": role.Version, "assignments_count": role.AssignmentsCount,
		"outdated_assignments_count": role.OutdatedAssignmentsCount, "permissions": permissions, "created_at": role.CreatedAt,
	}}, "success")
}

func (h *TenantRbacHandler) CreateTenantRoleRevision(c *gin.Context) {
	const op = "iam.rbac.tenant_role.create_revision"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 8*time.Second)
	defer cancel()
	actorUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}
	roleID, err := uuid.Parse(strings.TrimSpace(c.Param("role_id")))
	if err != nil || roleID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	var request iamReq.CreateTenantRoleRevisionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	name := strings.TrimSpace(request.Name)
	description := strings.TrimSpace(request.Description)
	if name == "" || len(name) > 255 || len(description) > 1000 || request.RoleLevel < 4 || request.RoleLevel > 99 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	permissionIDs := make([]uuid.UUID, 0, len(request.PermissionIDs))
	seen := make(map[uuid.UUID]struct{}, len(request.PermissionIDs))
	for _, raw := range request.PermissionIDs {
		permissionID, parseErr := uuid.Parse(strings.TrimSpace(raw))
		if parseErr != nil || permissionID == uuid.Nil {
			apires.RespondBadRequest(c, "invalid request")
			return
		}
		if _, exists := seen[permissionID]; !exists {
			seen[permissionID] = struct{}{}
			permissionIDs = append(permissionIDs, permissionID)
		}
	}
	if len(permissionIDs) == 0 || len(permissionIDs) > 256 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	out, err := h.tenantRbacSvc.CreateTenantRoleRevision(ctx, &iamEntity.CreateTenantRoleRevision{
		ActorUserID: actorUserID, TenantID: tenantID, RoleID: roleID, ExpectedVersion: request.ExpectedVersion,
		Name: name, Description: description, RoleLevel: request.RoleLevel, PermissionIDs: permissionIDs,
	})
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "role not found")
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			apires.RespondForbidden(c, "action not allowed")
		case errors.Is(err, iamTaxonomy.ErrConflict):
			apires.RespondConflict(c, "role revision is stale")
		case errors.Is(err, iamTaxonomy.ErrPreconditionFailed):
			apires.RespondBadRequest(c, "invalid permission selection")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondCreated(c, gin.H{"id": out.RoleID, "version": out.Version, "name": out.Name, "description": out.Description, "role_level": out.RoleLevel}, "tenant role revision created")
}

func (h *TenantRbacHandler) UpgradeTenantRoleAssignments(c *gin.Context) {
	const op = "iam.rbac.tenant_role.upgrade_assignments"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 15*time.Second)
	defer cancel()
	actorUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}
	roleID, err := uuid.Parse(strings.TrimSpace(c.Param("role_id")))
	if err != nil || roleID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	out, err := h.tenantRbacSvc.UpgradeTenantRoleAssignments(ctx, &iamEntity.UpgradeTenantRoleAssignments{ActorUserID: actorUserID, TenantID: tenantID, RoleID: roleID})
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "role not found")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			apires.RespondForbidden(c, "action not allowed")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	apires.RespondSuccess(c, gin.H{"role_id": out.RoleID, "version": out.Version, "updated_assignments_count": out.UpdatedCount}, "tenant role assignments upgraded")
}

func (h *TenantRbacHandler) CreateTenantRole(c *gin.Context) {
	const op = "iam.rbac.tenant_role.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	actorUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	var request iamReq.CreateTenantRoleRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "invalid tenant role request")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	code := strings.ToLower(strings.TrimSpace(request.Code))
	name := strings.TrimSpace(request.Name)
	description := strings.TrimSpace(request.Description)
	if !tenantRoleCodePattern.MatchString(code) || code == "tenant_root" || name == "" || len(name) > 255 || len(description) > 1000 ||
		request.RoleLevel < 4 || request.RoleLevel > 99 {
		logger.HandlerWarn(c, op, nil, "tenant role fields are invalid")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	permissionIDs := make([]uuid.UUID, 0, len(request.PermissionIDs))
	seen := make(map[uuid.UUID]struct{}, len(request.PermissionIDs))
	for _, rawPermissionID := range request.PermissionIDs {
		permissionID, err := uuid.Parse(strings.TrimSpace(rawPermissionID))
		if err != nil || permissionID == uuid.Nil {
			logger.HandlerWarn(c, op, err, "invalid permission id")
			apires.RespondBadRequest(c, "invalid request")
			return
		}
		if _, exists := seen[permissionID]; exists {
			continue
		}
		seen[permissionID] = struct{}{}
		permissionIDs = append(permissionIDs, permissionID)
	}
	if len(permissionIDs) == 0 || len(permissionIDs) > 256 {
		logger.HandlerWarn(c, op, nil, "tenant role permission count is invalid")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	role, err := h.tenantRbacSvc.CreateTenantRole(ctx, &iamEntity.CreateTenantRole{
		ActorUserID:   actorUserID,
		TenantID:      tenantID,
		Code:          code,
		Name:          name,
		Description:   description,
		RoleLevel:     request.RoleLevel,
		PermissionIDs: permissionIDs,
	})
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrNotFound):
			logger.HandlerWarn(c, op, err, "tenant not found")
			apires.RespondNotFound(c, "not found")
		case errors.Is(err, iamTaxonomy.ErrAlreadyExists):
			logger.HandlerWarn(c, op, err, "tenant role already exists")
			apires.RespondConflict(c, "role already exists")
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			logger.HandlerWarn(c, op, err, "tenant role hierarchy denied")
			apires.RespondForbidden(c, "action not allowed")
		case errors.Is(err, iamTaxonomy.ErrPreconditionFailed):
			logger.HandlerWarn(c, op, err, "tenant role permission precondition failed")
			apires.RespondBadRequest(c, "invalid permission selection")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"id":          role.ID,
		"code":        role.Code,
		"name":        role.Name,
		"description": role.Description,
		"role_level":  role.RoleLevel,
		"version":     role.Version,
		"created_at":  role.CreatedAt,
	}, "tenant role created")
}
