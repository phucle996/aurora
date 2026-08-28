package iamHandler

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"controlplane/internal/config"

	iamEntity "controlplane/internal/iam/domain/entity"
	domainservice "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	requestdto "controlplane/internal/iam/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/context"
	"controlplane/pkg/geoip"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var (
	passwordLowercaseRegex = regexp.MustCompile(`[a-z]`)
	passwordUppercaseRegex = regexp.MustCompile(`[A-Z]`)
	passwordDigitRegex     = regexp.MustCompile(`[0-9]`)
	passwordSpecialRegex   = regexp.MustCompile(`[^A-Za-z0-9]`)
	usernameRegex          = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{5,63}$`)
)

func isStrongPassword(password string) bool {
	if len(password) < 8 {
		return false
	}
	if !passwordLowercaseRegex.MatchString(password) {
		return false
	}
	if !passwordUppercaseRegex.MatchString(password) {
		return false
	}
	if !passwordDigitRegex.MatchString(password) {
		return false
	}
	if !passwordSpecialRegex.MatchString(password) {
		return false
	}
	return true
}

type AuthHandler struct {
	authSvc domainservice.AuthService
	cfg     *config.Config
	geoIP   *geoip.Resolver
}

// NewAuthHandler tạo HTTP handler cho auth endpoints.
func NewAuthHandler(cfg *config.Config, authSvc domainservice.AuthService) *AuthHandler {
	geoResolver, _ := geoip.NewResolver("")
	return &AuthHandler{
		authSvc: authSvc,
		cfg:     cfg,
		geoIP:   geoResolver,
	}
}

// RegisterAccount godoc
// @Summary Register account
// @Description Đăng ký tài khoản người dùng mới.
// @Tags auth
// @Accept json
// @Produce json
// @Param payload body requestdto.RegisterRequest true "Register payload"
// @Success 201 {object} map[string]interface{} "account created"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 409 {object} map[string]interface{} "resource already exists"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/auth/register [post]
func (h *AuthHandler) RegisterAccount(c *gin.Context) {
	const op = "iam.auth.register"

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	var request requestdto.RegisterRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind register request failed")
		apires.RespondBadRequest(c, "Invalid request")
		return
	}

	username := strings.ToLower(strings.TrimSpace(request.Username))
	email := strings.ToLower(strings.TrimSpace(request.Email))
	// [COMMENT]: Password là opaque secret; không trim vì khoảng trắng có thể là ký tự hợp lệ do người dùng chọn.
	password := request.Password
	fullname := strings.TrimSpace(request.Fullname)

	// [COMMENT]: Chặn username chứa ký tự '@' để tránh conflict với format login username@tenant_domain.
	// Ký tự '@' được dùng làm separator để phân biệt login global và login tenant context.
	if !usernameRegex.MatchString(username) {
		logger.HandlerWarn(c, op, iamTaxonomy.ErrInvalidArgument, "username canonical format is invalid")
		apires.RespondBadRequest(c, "Username must be 6-64 lowercase letters, numbers, '_' or '-', and start with a letter or number.")
		return
	}

	var phone *string
	if request.Phone != nil && *request.Phone != "" {
		phone = request.Phone
	}

	var localeStr string
	if request.Location != nil {
		localeStr = strings.TrimSpace(*request.Location)
	}

	// [COMMENT]: Giải mã địa chỉ IP của client sang quốc gia tương ứng
	clientIP := c.ClientIP()
	if resolvedCountry := h.geoIP.Lookup(clientIP); resolvedCountry != "" {
		localeStr = resolvedCountry
	}

	var timezoneStr string
	if request.Timezone != nil {
		timezoneStr = strings.TrimSpace(*request.Timezone)
	}
	if !isStrongPassword(password) {
		logger.HandlerWarn(c, op, iamTaxonomy.ErrInvalidArgument, "register validation failed")
		apires.RespondBadRequest(c, "Invalid request")
		return
	}

	cmd := iamEntity.RegisterAccount{
		Username: username,
		Email:    email,
		Phone:    phone,
		Password: password,
		Fullname: fullname,
		Locale:   localeStr,
		Timezone: timezoneStr,
	}

	registration, err := h.authSvc.RegisterAccount(ctx, &cmd)
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument):
			logger.HandlerWarn(c, op, err, "register validation failed")
			apires.RespondBadRequest(c, "Invalid request")
			return
		case errors.Is(err, iamTaxonomy.ErrUserAlreadyExist):
			logger.HandlerWarn(c, op, err, "register conflict")
			apires.RespondConflict(c, "Account has been existed in aurora. Please using another username or email and try again.")
			return
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "Internal server error")
			return
		}
	}
	if !registration.VerificationDispatched {
		logger.HandlerWarn(c, op, iamTaxonomy.ErrAuthenticationUnavailable, "verification dispatch unavailable after identity commit")
	}

	logger.HandlerInfo(c, op, "account registered")
	apires.RespondCreated(c, nil, "account created")
}

// VerifyAccount godoc
// @Summary Verify and activate account
// @Description Xác thực tài khoản bằng One-Time Token (OTT) gửi qua email và gán vai trò platform_user mặc định.
// @Tags auth
// @Produce json
// @Accept json
// @Param payload body requestdto.VerifyAccountRequest true "Activation payload"
// @Success 200 {object} map[string]interface{} "account activated"
// @Failure 400 {object} map[string]interface{} "invalid request or expired token"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/auth/verify [post]
func (h *AuthHandler) VerifyAccount(c *gin.Context) {
	const op = "iam.auth.verify"

	// [COMMENT]: Khởi tạo context với timeout 5 giây để tránh treo request lâu trong môi trường HA/Cloud Native
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	var request requestdto.VerifyAccountRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "invalid activation request")
		apires.RespondBadRequest(c, "Invalid activation request")
		return
	}
	token := strings.TrimSpace(request.Token)
	userIDStr := strings.TrimSpace(request.UserID)
	eventIDStr := strings.TrimSpace(request.EventID)

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "user_id is not a valid UUID format")
		apires.RespondBadRequest(c, "Invalid user_id")
		return
	}
	eventID, err := uuid.Parse(eventIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "event_id is not a valid UUID format")
		apires.RespondBadRequest(c, "Invalid event_id")
		return
	}

	// [COMMENT]: Thực hiện xác minh token và kích hoạt tài khoản thông qua core service
	if err := h.authSvc.VerifyAccount(ctx, userID, eventID, token); err != nil {
		if errors.Is(err, iamTaxonomy.ErrTokenExpired) {
			logger.HandlerWarn(c, op, err, "activation token has expired or is invalid")
			apires.RespondBadRequest(c, "Token has expired or is invalid")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrUserNotFound) {
			logger.HandlerWarn(c, op, err, "user not found during activation")
			apires.RespondBadRequest(c, "User not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "Internal server error")
		return
	}

	logger.HandlerInfo(c, op, "account successfully verified and activated")
	apires.RespondSuccess(c, nil, "account activated successfully")
}
