package iamHandler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	domainservice "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"
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

// [COMMENT]: ListUsers trả về danh sách users thô từ logic phân cấp bảo mật
func (h *UserHandler) ListUsers(c *gin.Context) {
	const op = "iam.users.list"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Lấy level của caller trực tiếp từ x-user-level header do ACR/Gateway forward xuống
	callerLevelStr := strings.TrimSpace(c.GetHeader("x-user-level"))
	if callerLevelStr == "" {
		logger.HandlerWarn(c, op, nil, "unauthorized - missing x-user-level header")
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}

	callerLevel, err := strconv.ParseUint(callerLevelStr, 10, 8)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid caller level format")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	users, err := h.userSvc.ListUsers(ctx, uint8(callerLevel), limit, offset)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			logger.HandlerWarn(c, op, err, "forbidden action for user")
			apires.RespondForbidden(c, "forbidden")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	resp := make([]gin.H, 0, len(users))
	for _, u := range users {
		resp = append(resp, gin.H{
			"id":         u.ID.String(),
			"username":   u.Username,
			"email":      u.Email,
			"status":     string(u.Status),
			"created_at": u.CreatedAt,
			"updated_at": u.UpdatedAt,
		})
	}

	apires.RespondSuccess(c, gin.H{"users": resp}, "success")
}

// [COMMENT]: UpdateUserStatus thực hiện cập nhật trạng thái hoạt động (vô hiệu hóa) của user
func (h *UserHandler) UpdateUserStatus(c *gin.Context) {
	const op = "iam.users.update_status"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Lấy level của caller trực tiếp từ x-user-level header do ACR/Gateway forward xuống
	callerLevelStr := strings.TrimSpace(c.GetHeader("x-user-level"))
	if callerLevelStr == "" {
		logger.HandlerWarn(c, op, nil, "unauthorized - missing x-user-level header")
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}

	callerLevel, err := strconv.ParseUint(callerLevelStr, 10, 8)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid caller level format")
		apires.RespondBadRequest(c, "invalid request")
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

	if err := h.userSvc.UpdateUserStatus(ctx, uint8(callerLevel), targetUserID); err != nil {
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

// [COMMENT]: GetMyProfile trả về thông tin profile hiển thị của chính user đó (self-service, bypass permissions check)
func (h *UserHandler) GetMyProfile(c *gin.Context) {
	const op = "iam.users.get_my_profile"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Lấy userID trực tiếp từ x-user-id header do gateway forward xuống sau JWT validation
	userIDStr := strings.TrimSpace(c.GetHeader("x-user-id"))
	if userIDStr == "" {
		logger.HandlerWarn(c, op, nil, "unauthorized - missing x-user-id header")
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid user id format")
		apires.RespondBadRequest(c, "invalid request")
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
