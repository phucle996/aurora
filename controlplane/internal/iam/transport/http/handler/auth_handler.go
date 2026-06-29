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
	"controlplane/pkg/constant"
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

	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	var request requestdto.RegisterRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind register request failed")
		apires.RespondBadRequest(c, "Invalid request")
		return
	}

	username := strings.ToLower(strings.TrimSpace(request.Username))
	email := strings.ToLower(strings.TrimSpace(request.Email))
	password := strings.TrimSpace(request.Password)
	fullname := strings.TrimSpace(request.Fullname)

	// [COMMENT]: Chặn username chứa ký tự '@' để tránh conflict với format login username@tenant_domain.
	// Ký tự '@' được dùng làm separator để phân biệt login global và login tenant context.
	if strings.Contains(username, "@") {
		logger.HandlerWarn(c, op, iamTaxonomy.ErrInvalidArgument, "username must not contain '@'")
		apires.RespondBadRequest(c, "Username must not contain '@'. Use email field for email address.")
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

	user := iamEntity.User{
		Username: username,
		Email:    email,
		Phone:    phone,
	}
	profile := iamEntity.UserProfile{
		Fullname: fullname,
		Locale:   localeStr,
		Timezone: timezoneStr,
	}

	if err := h.authSvc.RegisterAccount(ctx, user, profile, password); err != nil {
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

	logger.HandlerInfo(c, op, "account registered")
	apires.RespondCreated(c, nil, "account created")
}

// VerifyAccount godoc
// @Summary Verify and activate account
// @Description Xác thực tài khoản bằng One-Time Token (OTT) gửi qua email và gán vai trò platform_user mặc định.
// @Tags auth
// @Produce json
// @Param token query string true "Mã kích hoạt tài khoản"
// @Param user_id query string true "Mã UUID của người dùng"
// @Success 200 {object} map[string]interface{} "account activated"
// @Failure 400 {object} map[string]interface{} "invalid request or expired token"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/auth/verify [get]
func (h *AuthHandler) VerifyAccount(c *gin.Context) {
	const op = "iam.auth.verify"

	// [COMMENT]: Khởi tạo context với timeout 5 giây để tránh treo request lâu trong môi trường HA/Cloud Native
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	token := strings.TrimSpace(c.Query("token"))
	userIDStr := strings.TrimSpace(c.Query("user_id"))

	// [COMMENT]: Kiểm tra tính hợp lệ sơ bộ của đầu vào (Input Validation)
	if token == "" || userIDStr == "" {
		logger.HandlerWarn(c, op, iamTaxonomy.ErrInvalidArgument, "missing token or user_id in query parameters")
		apires.RespondBadRequest(c, "Missing token or user_id")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "user_id is not a valid UUID format")
		apires.RespondBadRequest(c, "Invalid user_id")
		return
	}

	// [COMMENT]: Thực hiện xác minh token và kích hoạt tài khoản thông qua core service
	if err := h.authSvc.VerifyAccount(ctx, userID, token); err != nil {
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
