package iamHandler

import (
	"context"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamDto "controlplane/internal/iam/transport/http/dto"
	"controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type MfaHandler struct {
	mfaSvc iamSvcInterface.MfaService
}

// NewMfaHandler khởi tạo handler HTTP xử lý các yêu cầu liên quan tới trạng thái MFA
func NewMfaHandler(mfaSvc iamSvcInterface.MfaService) *MfaHandler {
	return &MfaHandler{
		mfaSvc: mfaSvc,
	}
}

func (h *MfaHandler) GetMyMfa(c *gin.Context) {
	const op = "iam.mfa.get_my_status"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	status, err := h.mfaSvc.GetSelfMfaStatus(ctx, userID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}
	apires.RespondSuccess(c, gin.H{
		"status":                   string(status.Status),
		"enabled_at":               status.EnabledAt,
		"recovery_codes_remaining": status.RecoveryCodesRemaining,
	}, "success")
}

func (h *MfaHandler) StartMyMfaSetup(c *gin.Context) {
	const op = "iam.mfa.start_setup"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	result, err := h.mfaSvc.StartSetup(ctx, userID)
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrMFAAlreadyEnabled):
			apires.RespondConflict(c, "mfa is already enabled")
		case errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable):
			apires.RespondInternalError(c, "authentication service unavailable")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error occurred")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{
		"setup_id":         result.SetupID.String(),
		"provisioning_uri": result.ProvisioningURI,
		"manual_secret":    result.ManualSecret,
		"expires_at":       result.ExpiresAt,
	}, "mfa setup started")
}

func (h *MfaHandler) ConfirmMyMfaSetup(c *gin.Context) {
	const op = "iam.mfa.confirm_setup"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	setupID, err := uuid.Parse(strings.TrimSpace(c.Param("setup_id")))
	if err != nil || setupID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid setup id")
		return
	}
	var request iamDto.MFACodeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apires.RespondBadRequest(c, "invalid mfa code")
		return
	}
	code := strings.TrimSpace(request.Code)
	if len(code) != 6 {
		apires.RespondBadRequest(c, "invalid mfa code")
		return
	}
	result, err := h.mfaSvc.ConfirmSetup(ctx, userID, setupID, code)
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrMFASetupExpired), errors.Is(err, iamTaxonomy.ErrMFAChallengeInvalid):
			apires.RespondBadRequest(c, "mfa setup expired or invalid")
		case errors.Is(err, iamTaxonomy.ErrMFAAlreadyEnabled):
			apires.RespondConflict(c, "mfa is already enabled")
		case errors.Is(err, iamTaxonomy.ErrMFAInvalidCode):
			apires.RespondBadRequest(c, "invalid mfa code")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error occurred")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{
		"status":         "enabled",
		"enabled_at":     result.EnabledAt,
		"recovery_codes": result.RecoveryCodes,
	}, "mfa enabled")
}

func (h *MfaHandler) RegenerateMyRecoveryCodes(c *gin.Context) {
	const op = "iam.mfa.regenerate_recovery_codes"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	var request iamDto.MFACodeRequest
	if err := c.ShouldBindJSON(&request); err != nil || len(strings.TrimSpace(request.Code)) != 6 {
		apires.RespondBadRequest(c, "invalid mfa code")
		return
	}
	codes, err := h.mfaSvc.RegenerateRecoveryCodes(ctx, userID, strings.TrimSpace(request.Code))
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrMFAInvalidCode):
			apires.RespondUnauthorized(c, "invalid mfa code")
		case errors.Is(err, iamTaxonomy.ErrPreconditionFailed):
			apires.RespondBadRequest(c, "mfa is not enabled")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error occurred")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{"recovery_codes": codes}, "recovery codes regenerated")
}

func (h *MfaHandler) RemoveMyMfa(c *gin.Context) {
	const op = "iam.mfa.remove"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	var request iamDto.MFACodeRequest
	if err := c.ShouldBindJSON(&request); err != nil || len(strings.TrimSpace(request.Code)) != 6 {
		apires.RespondBadRequest(c, "invalid mfa code")
		return
	}
	if err := h.mfaSvc.Remove(ctx, userID, strings.TrimSpace(request.Code)); err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrMFAInvalidCode):
			apires.RespondUnauthorized(c, "invalid mfa code")
		case errors.Is(err, iamTaxonomy.ErrPreconditionFailed):
			apires.RespondBadRequest(c, "mfa is not enabled")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error occurred")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{"status": "removed"}, "mfa removed")
}

// GetUserMfaPlatform xử lý API lấy thông tin cấu hình MFA của một user bất kỳ dành cho platform admin/auditor
func (h *MfaHandler) GetUserMfaPlatform(c *gin.Context) {
	const op = "iam.mfa.get_user_mfa_platform"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	callerLevel, ok := pkgcontext.GetUserLevel(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Trích xuất user ID từ tham số path URL
	targetUserIDStr := c.Param("id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid user id")
		return
	}

	// [COMMENT]: Repository áp dụng hierarchy fence: role_level lớn hơn callerLevel mới được xem.
	enabled, createdAt, err := h.mfaSvc.GetUserMfaStatus(ctx, targetUserID, callerLevel)
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrUserNotFound):
			apires.RespondNotFound(c, "user not found")
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			apires.RespondForbidden(c, "target user is outside your hierarchy")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to query user mfa status")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"mfa_enabled": enabled,
		"created_at":  createdAt,
	}, "success")
}
