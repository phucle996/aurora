package iamHandler

import (
	"context"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	"controlplane/pkg/apires"
	"controlplane/pkg/context"
	"controlplane/pkg/logger"
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

// GetUserMfaPlatform xử lý API lấy thông tin cấu hình MFA của một user bất kỳ dành cho platform admin/auditor
func (h *MfaHandler) GetUserMfaPlatform(c *gin.Context) {
	const op = "iam.mfa.get_user_mfa_platform"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất user ID từ tham số path URL
	targetUserIDStr := c.Param("id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid user id")
		return
	}

	// [COMMENT]: Gọi service truy xuất trạng thái MFA từ database
	enabled, createdAt, err := h.mfaSvc.GetUserMfaStatus(ctx, targetUserID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to query user mfa status")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"mfa_enabled": enabled,
		"created_at":  createdAt,
	}, "success")
}
