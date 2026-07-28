package iamHandler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	domainservice "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type UserHandler struct {
	userSvc domainservice.UserService
}

func NewUserHandler(userSvc domainservice.UserService) *UserHandler {
	return &UserHandler{
		userSvc: userSvc,
	}
}

// [COMMENT]: ListUsersPlatform trả về danh sách users thô từ logic phân cấp bảo mật
func (h *UserHandler) ListUsersPlatform(c *gin.Context) {
	const op = "iam.users.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	callerLevel, ok := pkgcontext.GetUserLevel(c, op)
	if !ok {
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	users, err := h.userSvc.ListUsers(ctx, callerLevel, limit, offset)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	resp := make([]gin.H, 0, len(users))
	for _, u := range users {
		resp = append(resp, gin.H{
			"id":            u.ID.String(),
			"username":      u.Username,
			"email":         u.Email,
			"status":        string(u.Status),
			"role":          u.RoleName,
			"mfa_enabled":   u.MfaEnabled,
			"devices_count": u.DevicesCount,
			"bio":           u.Bio,
			"fullname":      u.Fullname,
			"last_seen_ip":  u.LastSeenIP, // [COMMENT]: IP thực tế từ device hoạt động gần nhất
			"last_seen_at":  u.LastSeenAt, // [COMMENT]: Thời điểm hoạt động gần nhất từ device
			"created_at":    u.CreatedAt,
			"updated_at":    u.UpdatedAt,
		})
	}

	apires.RespondSuccess(c, gin.H{"users": resp}, "success")
}

func (h *UserHandler) GetUserAuthMethodsPlatform(c *gin.Context) {
	const op = "iam.users.auth_methods"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// ContextInjector has already parsed the trusted X-User-Level injected by
	// ACR/Envoy. Never accept caller level or identity from query/body fields.
	callerLevel, ok := pkgcontext.GetUserLevel(c, op)
	if !ok {
		return
	}
	targetUserID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil || targetUserID == uuid.Nil {
		logger.HandlerWarn(c, op, err, "invalid target user id")
		apires.RespondBadRequest(c, "invalid user id")
		return
	}
	methods, err := h.userSvc.GetUserAuthMethods(ctx, callerLevel, targetUserID)
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrUserNotFound):
			apires.RespondNotFound(c, "user not found")
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			apires.RespondForbidden(c, "target user is outside your hierarchy")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error occurred")
		}
		return
	}

	external := func(summary iamEntity.ExternalIdentitySummary) gin.H {
		return gin.H{
			"provider":          string(summary.Provider),
			"state":             string(summary.State),
			"provider_email":    summary.ProviderEmail,
			"email_verified_at": summary.EmailVerifiedAt,
			"last_login_at":     summary.LastLoginAt,
			"linked_at":         summary.LinkedAt,
		}
	}
	apires.RespondSuccess(c, gin.H{
		"account_identifier_email": methods.AccountEmail,
		"password_set":             methods.PasswordSet,
		"google":                   external(methods.Google),
		"github":                   external(methods.GitHub),
	}, "success")
}

// [COMMENT]: UpdateUserStatusPlatform thực hiện cập nhật trạng thái hoạt động (vô hiệu hóa) của user
func (h *UserHandler) UpdateUserStatusPlatform(c *gin.Context) {
	const op = "iam.users.update_status"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	callerLevel, ok := pkgcontext.GetUserLevel(c, op)
	if !ok {
		return
	}

	targetUserIDStr := strings.TrimSpace(c.Param("id"))
	if targetUserIDStr == "" {
		logger.HandlerWarn(c, op, nil, "missing target user id param")
		apires.RespondBadRequest(c, "missing user id parameter")
		return
	}

	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid target user id format")
		apires.RespondBadRequest(c, "invalid user id parameter")
		return
	}

	// [COMMENT]: Nhận và parse strict trạng thái status truyền lên từ query params để tránh lệch mọi tình huống
	statusStr := strings.TrimSpace(c.Query("status"))
	var status iamEntity.UserStatus
	switch statusStr {
	case string(iamEntity.UserStatusPendingActive):
		status = iamEntity.UserStatusPendingActive
	case string(iamEntity.UserStatusActive):
		status = iamEntity.UserStatusActive
	case string(iamEntity.UserStatusSuspended):
		status = iamEntity.UserStatusSuspended
	case string(iamEntity.UserStatusDisabled):
		status = iamEntity.UserStatusDisabled
	default:
		logger.HandlerWarn(c, op, nil, "missing or invalid status parameter")
		apires.RespondBadRequest(c, "invalid or missing status parameter")
		return
	}

	if err := h.userSvc.UpdateUserStatus(ctx, callerLevel, targetUserID, string(status)); err != nil {
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			logger.HandlerWarn(c, op, err, "insufficient role hierarchy permissions")
			apires.RespondForbidden(c, "forbidden")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrUserNotFound) {
			logger.HandlerWarn(c, op, err, "user not found")
			apires.RespondNotFound(c, "user not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	apires.RespondSuccess(c, nil, "user status updated successfully")
}

// [COMMENT]: ResetUserPasswordPlatform thực hiện reset mật khẩu của user bởi Admin
func (h *UserHandler) ResetUserPasswordPlatform(c *gin.Context) {
	const op = "iam.users.reset_password"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	callerLevel, ok := pkgcontext.GetUserLevel(c, op)
	if !ok {
		return
	}

	targetUserIDStr := strings.TrimSpace(c.Param("id"))
	if targetUserIDStr == "" {
		apires.RespondBadRequest(c, "missing user id parameter")
		return
	}
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid user id format")
		return
	}

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	if err := h.userSvc.ResetUserPassword(ctx, callerLevel, targetUserID, req.Password); err != nil {
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			logger.HandlerWarn(c, op, err, "insufficient role hierarchy permissions")
			apires.RespondForbidden(c, "forbidden")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrUserNotFound) {
			logger.HandlerWarn(c, op, err, "user not found")
			apires.RespondNotFound(c, "user not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	apires.RespondSuccess(c, nil, "user password reset successfully")
}

// [COMMENT]: GetMyProfile trả về thông tin profile hiển thị của chính user đó (self-service, bypass permissions check)
func (h *UserHandler) GetMyProfile(c *gin.Context) {
	const op = "iam.users.get_my_profile"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	profile, err := h.userSvc.GetUserProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrUserNotFound) {
			logger.HandlerWarn(c, op, err, "profile not found")
			apires.RespondNotFound(c, "profile not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"user_id":    profile.UserID.String(),
		"fullname":   profile.Fullname,
		"avatar_url": profile.AvatarURL,
		"bio":        profile.Bio,
		"locale":     profile.Locale,
		"timezone":   profile.Timezone,
	}, "success")
}
