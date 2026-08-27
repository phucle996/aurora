package iamHandler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	iamEntity "controlplane/internal/iam/domain/entity"
	domainservice "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamDto "controlplane/internal/iam/transport/http/dto"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/text/language"
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

	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, offsetErr := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offsetErr != nil || limit < 1 || limit > 100 || offset < 0 {
		apires.RespondBadRequest(c, "invalid pagination")
		return
	}

	users, err := h.userSvc.ListUsers(ctx, iamEntity.ListUsers{
		CallerLevel: callerLevel,
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	resp := make([]gin.H, 0, len(users))
	for _, user := range users {
		resp = append(resp, gin.H{
			"id":            user.ID.String(),
			"username":      user.Username,
			"email":         user.Email,
			"status":        user.Status,
			"role":          user.RoleName,
			"mfa_enabled":   user.MFAEnabled,
			"devices_count": user.DevicesCount,
			"bio":           user.Bio,
			"fullname":      user.Fullname,
			"last_seen_ip":  user.LastSeenIP,
			"last_seen_at":  user.LastSeenAt,
			"created_at":    user.CreatedAt,
			"updated_at":    user.UpdatedAt,
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
	methods, err := h.userSvc.GetUserAuthMethods(ctx, iamEntity.GetUserAuthMethods{
		CallerLevel: callerLevel,
		UserID:      targetUserID,
	})
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

	external := func(method iamEntity.GetUserAuthMethods) gin.H {
		return gin.H{
			"provider":          method.Provider,
			"state":             method.State,
			"provider_email":    method.ProviderEmail,
			"email_verified_at": method.EmailVerifiedAt,
			"last_login_at":     method.LastLoginAt,
			"linked_at":         method.LinkedAt,
		}
	}
	response := gin.H{
		"account_identifier_email": "",
		"password_set":             false,
		"google":                   gin.H{},
		"github":                   gin.H{},
	}
	for _, method := range methods {
		response["account_identifier_email"] = method.AccountEmail
		response["password_set"] = method.PasswordSet
		switch method.Provider {
		case "google":
			response["google"] = external(method)
		case "github":
			response["github"] = external(method)
		}
	}
	apires.RespondSuccess(c, response, "success")
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

	var req iamDto.UpdateUserStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	// [COMMENT]: Request body is already covered by session proof. This is only
	// the transport boundary's format validation before the business service.
	statusStr := strings.TrimSpace(req.Status)
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

	if err := h.userSvc.UpdateUserStatus(ctx, iamEntity.UpdateUserStatus{
		CallerLevel:  callerLevel,
		TargetUserID: targetUserID,
		Status:       string(status),
	}); err != nil {
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

	var req iamDto.ResetUserPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}
	if !isStrongPassword(req.Password) {
		apires.RespondBadRequest(c, "invalid password")
		return
	}

	if err := h.userSvc.ResetUserPassword(ctx, iamEntity.ResetUserPassword{
		OperationID:  uuid.New(),
		CallerLevel:  callerLevel,
		TargetUserID: targetUserID,
		Password:     req.Password,
	}); err != nil {
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

	profile := &iamEntity.GetMyProfile{UserID: userID}
	err := h.userSvc.GetMyProfile(ctx, profile)
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
		"user_id":       profile.UserID.String(),
		"username":      profile.Username,
		"account_email": profile.AccountEmail,
		"phone":         profile.Phone,
		"fullname":      profile.Fullname,
		"address":       profile.Address,
		"avatar_url":    profile.AvatarURL,
		"bio":           profile.Bio,
		"locale":        profile.Locale,
		"timezone":      profile.Timezone,
	}, "success")
}

func (h *UserHandler) UpdateMyProfile(c *gin.Context) {
	const op = "iam.users.update_my_profile"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	var request iamDto.UpdateMyProfileRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		apires.RespondBadRequest(c, "request body must contain exactly one JSON object")
		return
	}

	request.Fullname = strings.TrimSpace(request.Fullname)
	request.Phone = strings.TrimSpace(request.Phone)
	request.Address = strings.TrimSpace(request.Address)
	request.AvatarURL = strings.TrimSpace(request.AvatarURL)
	request.Bio = strings.TrimSpace(request.Bio)
	request.Locale = strings.TrimSpace(request.Locale)
	request.Timezone = strings.TrimSpace(request.Timezone)

	if request.Fullname == "" || len(request.Fullname) > 120 ||
		len(request.Phone) > 32 || len(request.Address) > 500 ||
		len(request.AvatarURL) > 2048 || len(request.Bio) > 500 ||
		len(request.Locale) == 0 || len(request.Locale) > 16 ||
		len(request.Timezone) == 0 || len(request.Timezone) > 64 {
		apires.RespondBadRequest(c, "invalid profile fields")
		return
	}
	if strings.IndexFunc(request.Fullname, unicode.IsControl) >= 0 ||
		strings.IndexFunc(request.Address, unicode.IsControl) >= 0 ||
		strings.IndexFunc(request.AvatarURL, unicode.IsControl) >= 0 ||
		strings.IndexFunc(request.Bio, unicode.IsControl) >= 0 ||
		strings.IndexFunc(request.Locale, unicode.IsControl) >= 0 ||
		strings.IndexFunc(request.Timezone, unicode.IsControl) >= 0 {
		apires.RespondBadRequest(c, "invalid profile fields")
		return
	}
	if request.Phone != "" {
		validPhone := strings.HasPrefix(request.Phone, "+") && len(request.Phone) >= 8 && len(request.Phone) <= 16 &&
			request.Phone[1] >= '1' && request.Phone[1] <= '9'
		for index, char := range request.Phone {
			if index == 0 {
				continue
			}
			if char < '0' || char > '9' {
				validPhone = false
				break
			}
		}
		if !validPhone {
			apires.RespondBadRequest(c, "invalid phone")
			return
		}
	}
	if request.AvatarURL != "" {
		avatarURL, err := url.Parse(request.AvatarURL)
		if err != nil || avatarURL.Scheme != "https" || avatarURL.Host == "" ||
			avatarURL.User != nil || avatarURL.RawQuery != "" || avatarURL.Fragment != "" {
			apires.RespondBadRequest(c, "invalid avatar_url")
			return
		}
	}
	if _, err := language.Parse(request.Locale); err != nil {
		apires.RespondBadRequest(c, "invalid locale")
		return
	}
	if _, err := time.LoadLocation(request.Timezone); err != nil {
		apires.RespondBadRequest(c, "invalid timezone")
		return
	}

	var phone, address, avatarURL, bio *string
	if request.Phone != "" {
		phone = &request.Phone
	}
	if request.Address != "" {
		address = &request.Address
	}
	if request.AvatarURL != "" {
		avatarURL = &request.AvatarURL
	}
	if request.Bio != "" {
		bio = &request.Bio
	}
	updated := &iamEntity.UpdateMyProfile{
		UserID:    userID,
		Phone:     phone,
		Fullname:  request.Fullname,
		Address:   address,
		AvatarURL: avatarURL,
		Bio:       bio,
		Locale:    request.Locale,
		Timezone:  request.Timezone,
	}
	err := h.userSvc.UpdateMyProfile(ctx, updated)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrUserNotFound) {
			apires.RespondNotFound(c, "profile not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"user_id":       updated.UserID.String(),
		"username":      updated.Username,
		"account_email": updated.AccountEmail,
		"phone":         updated.Phone,
		"fullname":      updated.Fullname,
		"address":       updated.Address,
		"avatar_url":    updated.AvatarURL,
		"bio":           updated.Bio,
		"locale":        updated.Locale,
		"timezone":      updated.Timezone,
	}, "profile updated")
}

func (h *UserHandler) GetMySocialLinks(c *gin.Context) {
	const op = "iam.users.get_my_social_links"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	links, err := h.userSvc.GetMySocialLinks(ctx, &iamEntity.GetMySocialLinks{UserID: userID})
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrUserNotFound) {
			apires.RespondNotFound(c, "social links not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	items := make([]gin.H, 0, len(links))
	for _, link := range links {
		items = append(items, gin.H{
			"provider":          link.Provider,
			"state":             link.State,
			"provider_email":    link.ProviderEmail,
			"email_verified_at": link.EmailVerifiedAt,
			"last_login_at":     link.LastLoginAt,
			"linked_at":         link.LinkedAt,
		})
	}
	apires.RespondSuccess(c, gin.H{"items": items}, "success")
}

func (h *UserHandler) UnlinkMySocialLink(c *gin.Context) {
	const op = "iam.users.unlink_my_social_link"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(c.Param("provider")))
	var externalProvider string
	switch provider {
	case "google", "github":
		externalProvider = provider
	default:
		apires.RespondBadRequest(c, "invalid social provider")
		return
	}

	if err := h.userSvc.UnlinkMySocialLink(ctx, iamEntity.UnlinkMySocialLink{
		OperationID: uuid.New(),
		UserID:      userID,
		Provider:    externalProvider,
	}); err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable):
			apires.RespondServiceUnavailable(c, "authentication service unavailable")
		case errors.Is(err, iamTaxonomy.ErrUserNotFound):
			apires.RespondNotFound(c, "user not found")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error occurred")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{"provider": provider, "state": "not_linked"}, "social link removed")
}
