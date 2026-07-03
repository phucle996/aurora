package iamHandler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamReq "controlplane/internal/iam/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RbacHandler xử lý các HTTP endpoints cho IAM RBAC.
type RbacHandler struct {
	rbacSvc iamSvcInterface.RbacService
}

// NewRbacHandler tạo HTTP handler cho RBAC endpoints.
func NewRbacHandler(rbacSvc iamSvcInterface.RbacService) *RbacHandler {
	return &RbacHandler{rbacSvc: rbacSvc}
}

// ListRoles godoc
// @Summary List RBAC roles
// @Description Trả danh sách role RBAC của hệ thống.
// @Tags rbac
// @Produce json
// @Success 200 {object} map[string]interface{} "ok"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 404 {object} map[string]interface{} "resource not found"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/rbac/roles [get]
func (h *RbacHandler) ListRoles(c *gin.Context) {
	const op = "iam.rbac.list_roles"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	levelStr := strings.TrimSpace(c.GetHeader(constant.HeaderXUserLevel))
	level, err := strconv.Atoi(levelStr)
	if levelStr == "" || err != nil {
		logger.HandlerWarn(c, op, nil, "rbac unauthorized")
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}

	roles, err := h.rbacSvc.ListRoles(ctx, level)
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument):
			logger.HandlerWarn(c, op, err, "rbac invalid argument")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, iamTaxonomy.ErrNotFound), errors.Is(err, iamTaxonomy.ErrPermissionNotFound):
			logger.HandlerWarn(c, op, err, "rbac resource not found")
			apires.RespondNotFound(c, "resource not found")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondSuccess(c, roles, "ok")
}

// CreateRole godoc
// @Summary Create RBAC role
// @Description Tạo role RBAC mới ở scope platform.
// @Tags rbac
// @Accept json
// @Produce json
// @Param payload body iamReq.CreateRoleRequest true "Create role payload"
// @Success 201 {object} map[string]interface{} "created"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 404 {object} map[string]interface{} "resource not found"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/rbac/roles [post]
func (h *RbacHandler) CreateRole(c *gin.Context) {
	const op = "iam.rbac.create_role"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	levelStr := strings.TrimSpace(c.GetHeader(constant.HeaderXUserLevel))
	level, err := strconv.Atoi(levelStr)
	if levelStr == "" || err != nil {
		logger.HandlerWarn(c, op, nil, "rbac unauthorized")
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}

	var req iamReq.CreateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "bind failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	role := &iamEntity.Role{
		Code:      strings.TrimSpace(strings.ToLower(req.Code)),
		Name:      strings.TrimSpace(req.Name),
		ScopeType: iamEntity.RoleScopeTypePlatform,
	}
	if err := h.rbacSvc.CreateRole(ctx, level, role); err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument):
			logger.HandlerWarn(c, op, err, "rbac invalid argument")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, iamTaxonomy.ErrNotFound), errors.Is(err, iamTaxonomy.ErrPermissionNotFound):
			logger.HandlerWarn(c, op, err, "rbac resource not found")
			apires.RespondNotFound(c, "resource not found")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondCreated(c, nil, "created")
}

// UpdateRole godoc
// @Summary Update RBAC role
// @Description Cập nhật thông tin role RBAC theo ID.
// @Tags rbac
// @Accept json
// @Produce json
// @Param id path string true "Role ID"
// @Param payload body iamReq.UpdateRoleRequest true "Update role payload"
// @Success 200 {object} map[string]interface{} "updated"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 404 {object} map[string]interface{} "resource not found"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/rbac/roles/{id} [put]
func (h *RbacHandler) UpdateRole(c *gin.Context) {
	const op = "iam.rbac.update_role"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	levelStr := strings.TrimSpace(c.GetHeader(constant.HeaderXUserLevel))
	actorLevel, err := strconv.Atoi(levelStr)
	if levelStr == "" || err != nil {
		logger.HandlerWarn(c, op, nil, "rbac unauthorized")
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}

	idStr := strings.TrimSpace(c.Param("id"))
	id, err := uuid.Parse(idStr)
	if err != nil || id == uuid.Nil {
		logger.HandlerWarn(c, op, err, "invalid role id")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	var req iamReq.UpdateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "bind failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	role, err := h.rbacSvc.GetRole(ctx, actorLevel, id)
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument):
			logger.HandlerWarn(c, op, err, "rbac invalid argument")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, iamTaxonomy.ErrNotFound), errors.Is(err, iamTaxonomy.ErrPermissionNotFound):
			logger.HandlerWarn(c, op, err, "rbac resource not found")
			apires.RespondNotFound(c, "resource not found")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	if err := h.rbacSvc.UpdateRole(ctx, actorLevel, role.Role); err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument):
			logger.HandlerWarn(c, op, err, "rbac invalid argument")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, iamTaxonomy.ErrNotFound), errors.Is(err, iamTaxonomy.ErrPermissionNotFound):
			logger.HandlerWarn(c, op, err, "rbac resource not found")
			apires.RespondNotFound(c, "resource not found")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondSuccess(c, nil, "updated")
}

// DeleteRole godoc
// @Summary Delete RBAC role
// @Description Xóa role RBAC theo ID.
// @Tags rbac
// @Produce json
// @Param id path string true "Role ID"
// @Success 204 {string} string "No Content"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 404 {object} map[string]interface{} "resource not found"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/rbac/roles/{id} [delete]
func (h *RbacHandler) DeleteRole(c *gin.Context) {
	const op = "iam.rbac.delete_role"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	levelStr := strings.TrimSpace(c.GetHeader(constant.HeaderXUserLevel))
	actorLevel, err := strconv.Atoi(levelStr)
	if levelStr == "" || err != nil {
		logger.HandlerWarn(c, op, nil, "rbac unauthorized")
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}

	idStr := strings.TrimSpace(c.Param("id"))
	id, err := uuid.Parse(idStr)
	if err != nil || id == uuid.Nil {
		logger.HandlerWarn(c, op, err, "invalid role id")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	if err := h.rbacSvc.DeleteRole(ctx, actorLevel, id); err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument):
			logger.HandlerWarn(c, op, err, "rbac invalid argument")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, iamTaxonomy.ErrNotFound), errors.Is(err, iamTaxonomy.ErrPermissionNotFound):
			logger.HandlerWarn(c, op, err, "rbac resource not found")
			apires.RespondNotFound(c, "resource not found")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	c.Status(http.StatusNoContent)
}
