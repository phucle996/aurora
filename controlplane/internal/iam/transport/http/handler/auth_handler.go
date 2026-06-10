package iamHandler

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"controlplane/internal/config"
	"controlplane/internal/http/middleware"
	iamEntity "controlplane/internal/iam/domain/entity"
	domainservice "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	requestdto "controlplane/internal/iam/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	cookie "controlplane/pkg/constant"
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
}

// NewAuthHandler tạo HTTP handler cho auth endpoints.
func NewAuthHandler(cfg *config.Config, authSvc domainservice.AuthService) *AuthHandler {
	return &AuthHandler{authSvc: authSvc, cfg: cfg}
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var request requestdto.RegisterRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind register request failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	username := strings.ToLower(strings.TrimSpace(request.Username))
	email := strings.ToLower(strings.TrimSpace(request.Email))
	password := strings.TrimSpace(request.Password)
	rePassword := strings.TrimSpace(request.RePassword)
	fullname := strings.TrimSpace(request.Fullname)

	var phone *string
	if request.Phone != nil && *request.Phone != "" {
		phone = request.Phone
	}

	var localeStr string
	if request.Location != nil {
		localeStr = strings.TrimSpace(*request.Location)
	}

	var timezoneStr string
	if request.Timezone != nil {
		timezoneStr = strings.TrimSpace(*request.Timezone)
	}
	if password != rePassword {
		logger.HandlerWarn(c, op, iamTaxonomy.ErrInvalidArgument, "register validation failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	if !isStrongPassword(password) {
		logger.HandlerWarn(c, op, iamTaxonomy.ErrInvalidArgument, "register validation failed")
		apires.RespondBadRequest(c, "invalid request")
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
			apires.RespondBadRequest(c, "invalid request")
			return
		case errors.Is(err, iamTaxonomy.ErrUserAlreadyExist):
			logger.HandlerWarn(c, op, err, "register conflict")
			apires.RespondConflict(c, "resource already exists")
			return
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
			return
		}
	}

	logger.HandlerInfo(c, op, "account registered")
	apires.RespondCreated(c, nil, "account created")
}

// Login godoc
// @Summary Login
// @Description Đăng nhập bằng username và password, set access/refresh cookies khi thành công.
// @Tags auth
// @Accept json
// @Produce json
// @Param payload body requestdto.LoginRequest true "Login payload"
// @Success 200 {object} map[string]interface{} "login successful"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 401 {object} map[string]interface{} "invalid credentials"
// @Failure 403 {object} map[string]interface{} "please check your email to verify account"
// @Failure 503 {object} map[string]interface{} "authentication temporarily unavailable"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	const op = "iam.auth.login"

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var request requestdto.LoginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind login request failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	username := strings.ToLower(strings.TrimSpace(request.Username))
	password := strings.TrimSpace(request.Password)
	devicePublicKey := strings.TrimSpace(request.DevicePublicKey)
	if username == "" || password == "" || devicePublicKey == "" {
		logger.HandlerWarn(c, op, iamTaxonomy.ErrInvalidArgument, "login validation failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	var requestIP *string
	if ip := strings.TrimSpace(c.ClientIP()); ip != "" {
		requestIP = &ip
	}
	var userAgent *string
	if ua := strings.TrimSpace(c.Request.UserAgent()); ua != "" {
		userAgent = &ua
	}

	hostnameHint := c.GetHeader("X-Device-Hostname")
	hostnameAlias := c.GetHeader("X-Device-Name")
	deviceName := resolveDeviceName(hostnameHint, hostnameAlias)

	clientDeviceIDHintStr := c.GetHeader("X-Client-Device-Id")
	if clientDeviceIDHintStr == "" {
		if cookieValue, _ := c.Cookie(cookie.ClientDeviceIDName); cookieValue != "" {
			clientDeviceIDHintStr = cookieValue
		}
	}
	clientDeviceIDHint, err := uuid.Parse(clientDeviceIDHintStr)
	if err != nil {
		clientDeviceIDHint = uuid.Nil
	}

	result, err := h.authSvc.Login(ctx, iamEntity.LoginRequest{
		Username:        username,
		Password:        password,
		DevicePublicKey: devicePublicKey,
		IP:              requestIP,
		UserAgent:       userAgent,
		DeviceName:      deviceName,
		ClientDeviceID:  clientDeviceIDHint,
	})
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidCredentials):
			logger.HandlerWarn(c, op, err, "login invalid credentials")
			apires.RespondUnauthorized(c, "invalid credentials")
			return
		case errors.Is(err, iamTaxonomy.ErrVerificationRequired):
			logger.HandlerWarn(c, op, err, "login verification required")
			apires.RespondForbidden(c, "please check your email to verify account")
			return
		case errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable):
			logger.HandlerWarn(c, op, err, "login authentication unavailable")
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			return
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
			return
		}
	}

	secure := c.Request.TLS != nil
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.AccessTokenName,
		Value:    result.AccessToken,
		Path:     "/",
		Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.AccessExpiresAt,
		MaxAge:   int(time.Until(result.AccessExpiresAt).Seconds()),
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.RefreshTokenName,
		Value:    result.RefreshToken,
		Path:     "/",
		Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.RefreshExpiresAt,
		MaxAge:   int(time.Until(result.RefreshExpiresAt).Seconds()),
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.AccessKeyName,
		Value:    result.AccessKey,
		Path:     "/",
		Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.AccessExpiresAt,
		MaxAge:   int(time.Until(result.AccessExpiresAt).Seconds()),
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.AccessSecretName,
		Value:    result.AccessSecret,
		Path:     "/",
		Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.AccessExpiresAt,
		MaxAge:   int(time.Until(result.AccessExpiresAt).Seconds()),
	})
	cdidExpires := time.Now().UTC().Add(365 * 24 * time.Hour)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.ClientDeviceIDName,
		Value:    result.ClientDeviceID,
		Path:     "/",
		Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  cdidExpires,
		MaxAge:   int(time.Until(cdidExpires).Seconds()),
	})
	c.Header("X-Client-Device-Id", result.ClientDeviceID)

	logger.HandlerInfo(c, op, "login successful")
	apires.RespondSuccess(c, nil, "login successful")
}

// Session godoc
// @Summary User session bootstrap
// @Description Trả về trạng thái authenticated khi access cookie + device fragment hợp lệ.
// @Tags auth
// @Produce json
// @Success 200 {object} map[string]interface{} "authenticated"
// @Failure 401 {object} map[string]interface{} "unauthorized"
// @Failure 503 {object} map[string]interface{} "authentication temporarily unavailable"
// @Router /api/v1/auth/session [get]
func (h *AuthHandler) Session(c *gin.Context) {
	const op = "iam.auth.session"

	if strings.TrimSpace(middleware.GetUserID(c)) == "" || strings.TrimSpace(middleware.GetRuntimeAccessKey(c)) == "" {
		logger.HandlerWarn(c, op, iamTaxonomy.ErrInvalidCredentials, "session invalid auth context")
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}

	apires.RespondSuccess(c, gin.H{"authenticated": true}, "ok")
}

// Logout godoc
// @Summary User logout
// @Description Xoá runtime device, revoke refresh token và clear cookie session.
// @Tags auth
// @Produce json
// @Success 204 {string} string "No Content"
// @Failure 401 {object} map[string]interface{} "unauthorized"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	const op = "iam.auth.logout"

	// Clear cookies inline immediately so that the client's session is cleared in all execution paths
	domain := strings.TrimSpace(h.cfg.App.PublicDomain)
	secure := c.Request.TLS != nil
	exp := time.Unix(0, 0)
	for _, cookieDef := range []struct {
		name     string
		httpOnly bool
	}{
		{cookie.AccessTokenName, true},
		{cookie.RefreshTokenName, true},
		{cookie.AccessKeyName, false},
		{cookie.AccessSecretName, true},
	} {
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     cookieDef.name,
			Value:    "",
			Path:     "/",
			Domain:   domain,
			HttpOnly: cookieDef.httpOnly,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
			Expires:  exp,
		})
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userIDStr := strings.TrimSpace(middleware.GetUserID(c))
	if userIDStr == "" {
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}
	userID, parseErr := uuid.Parse(userIDStr)
	if parseErr != nil {
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}

	accessKey := strings.TrimSpace(middleware.GetRuntimeAccessKey(c))
	accessSecret := strings.TrimSpace(middleware.GetRuntimeAccessSecret(c))

	// Cho phép logout dù thiếu accessKey/secret (best-effort clear cookies đã xong ở đầu).
	if accessKey == "" {
		c.Status(http.StatusNoContent)
		return
	}

	if err := h.authSvc.Logout(ctx, userID, accessKey, accessSecret); err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	logger.HandlerInfo(c, op, "user logout successful")
	c.Status(http.StatusNoContent)
}
