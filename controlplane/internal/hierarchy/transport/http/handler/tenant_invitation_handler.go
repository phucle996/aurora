package hierarchyHandler

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	hierarchyReq "controlplane/internal/hierarchy/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var tenantInvitationUsernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{5,63}$`)

type TenantInvitationHandler struct {
	service hierarchySvcInterface.TenantInvitationService
}

func NewTenantInvitationHandler(service hierarchySvcInterface.TenantInvitationService) *TenantInvitationHandler {
	return &TenantInvitationHandler{service: service}
}

func (h *TenantInvitationHandler) CreateTenantInvitation(c *gin.Context) {
	const op = "hierarchy.tenant_invitation.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất User ID và Tenant ID thông qua pkgcontext helper được inject từ Middleware
	actorUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	var request hierarchyReq.CreateTenantInvitationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}
	identifier := strings.ToLower(strings.TrimSpace(request.Identifier))
	tenantRoleID, err := uuid.Parse(strings.TrimSpace(request.TenantRoleID))
	if err != nil || tenantRoleID == uuid.Nil || len(identifier) < 6 || len(identifier) > 320 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	targetByEmail := strings.Contains(identifier, "@")
	if targetByEmail {
		address, parseErr := mail.ParseAddress(identifier)
		if parseErr != nil || address.Address != identifier {
			apires.RespondBadRequest(c, "invalid request")
			return
		}
	} else if !tenantInvitationUsernamePattern.MatchString(identifier) {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	out, err := h.service.CreateTenantInvitation(ctx, &hierarchyEntity.CreateTenantInvitation{
		TenantID:         tenantID,
		InviterUserID:    actorUserID,
		TargetIdentifier: identifier,
		TargetByEmail:    targetByEmail,
		TenantRoleID:     tenantRoleID,
	})
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "resource not found")
		case errors.Is(err, hierarchyTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource already exists")
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			apires.RespondForbidden(c, "action is not allowed")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"id":             out.ID,
		"tenant_role_id": out.TenantRoleID,
		"role_code":      out.RoleCode,
		"role_name":      out.RoleName,
		"expires_at":     out.ExpiresAt,
		// [COMMENT]: This is the only response containing the bearer token; only
		// its SHA-256 digest crosses the durable PostgreSQL boundary.
		"join_link": "/settings/tenant-invitations/join?token=" + out.Token,
	}, "tenant invitation created")
}

func (h *TenantInvitationHandler) PreviewTenantInvitation(c *gin.Context) {
	const op = "hierarchy.tenant_invitation.preview"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 3*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất User ID thông qua pkgcontext helper
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tokenBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(c.Query("token")))
	if err != nil || len(tokenBytes) != 32 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	tokenHash := sha256.Sum256(tokenBytes)
	out, err := h.service.PreviewTenantInvitation(ctx, &hierarchyEntity.PreviewTenantInvitation{
		UserID:    userID,
		TokenHash: tokenHash[:],
	})
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "invitation not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"tenant_id":    out.TenantID,
		"tenant_code":  out.TenantCode,
		"tenant_name":  out.TenantName,
		"inviter_name": out.InviterName,
		"role_code":    out.RoleCode,
		"role_name":    out.RoleName,
		"role_level":   out.RoleLevel,
		"expires_at":   out.ExpiresAt,
	}, "tenant invitation")
}

func (h *TenantInvitationHandler) RevokeTenantInvitation(c *gin.Context) {
	const op = "hierarchy.tenant_invitation.revoke"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất User ID và Tenant ID thông qua pkgcontext helper
	actorUserID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}
	invitationID, err := uuid.Parse(strings.TrimSpace(c.Param("invitation_id")))
	if err != nil || invitationID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	out, err := h.service.RevokeTenantInvitation(ctx, &hierarchyEntity.RevokeTenantInvitation{
		ID: invitationID, TenantID: tenantID, ActorUserID: actorUserID,
	})
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "invitation not found")
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			apires.RespondForbidden(c, "action is not allowed")
		case errors.Is(err, hierarchyTaxonomy.ErrConflict):
			apires.RespondConflict(c, "resource conflict")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":             out.ID,
		"target_user_id": out.TargetUserID,
		"tenant_role_id": out.TenantRoleID,
	}, "tenant invitation revoked")
}

func (h *TenantInvitationHandler) JoinTenantInvitation(c *gin.Context) {
	const op = "hierarchy.tenant_invitation.join"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất User ID thông qua pkgcontext helper
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	var request hierarchyReq.JoinTenantInvitationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}
	tokenBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(request.Token))
	if err != nil || len(tokenBytes) != 32 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	tokenHash := sha256.Sum256(tokenBytes)
	out, err := h.service.JoinTenantInvitation(ctx, &hierarchyEntity.JoinTenantInvitation{
		UserID: userID, TokenHash: tokenHash[:],
	})
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "invitation not found")
		case errors.Is(err, hierarchyTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource already exists")
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			apires.RespondForbidden(c, "invitation is no longer valid")
		case errors.Is(err, hierarchyTaxonomy.ErrConflict):
			apires.RespondConflict(c, "resource conflict")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"tenant_id":      out.TenantID,
		"tenant_code":    out.TenantCode,
		"tenant_name":    out.TenantName,
		"tenant_role_id": out.TenantRoleID,
		"role_code":      out.RoleCode,
		"role_name":      out.RoleName,
		"role_level":     out.RoleLevel,
	}, "tenant joined")
}
