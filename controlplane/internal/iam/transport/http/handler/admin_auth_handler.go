package iamHandler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/config"
	deviceHint "controlplane/internal/iam/devicehint"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamReq "controlplane/internal/iam/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"
	cookie "controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

type AdminAuthHandler struct {
	svc iamSvcInterface.AdminAPIKeyService
	cfg *config.Config
}

// NewAdminAuthHandler tạo HTTP handler cho admin auth endpoints.
func NewAdminAuthHandler(cfg *config.Config,
	svc iamSvcInterface.AdminAPIKeyService,
) *AdminAuthHandler {
	return &AdminAuthHandler{
		svc: svc,
		cfg: cfg,
	}
}

// Login godoc
// @Summary Admin login bằng API key + MFA
// @Description Xác thực admin bằng admin_api_key, mfa_method, mfa_code và device_public_key; success sẽ set cookie admin runtime.
// @Tags admin-auth
// @Accept json
// @Produce json
// @Param payload body iamReq.AdminLoginRequest true "Admin login payload"
// @Success 200 {object} map[string]interface{} "ok"
// @Failure 400 {object} map[string]interface{} "invalid request"
// @Failure 401 {object} map[string]interface{} "unauthorized"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/auth/login [post]
func (h *AdminAuthHandler) Login(c *gin.Context) {
	const op = "iam.admin_auth.login"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var request iamReq.AdminLoginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind admin login request failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	hostnameHint := c.GetHeader(deviceHint.HeaderDeviceHostname)
	hostnameAlias := c.GetHeader(deviceHint.HeaderDeviceNameAlt)
	clientDeviceIDHint := c.GetHeader(deviceHint.HeaderClientDeviceID)
	if clientDeviceIDHint == "" {
		if cookieValue, _ := c.Cookie(cookie.ClientDeviceIDName); cookieValue != "" {
			clientDeviceIDHint = cookieValue
		}
	}

	// call to service
	result, err := h.svc.AdminLogin(ctx, iamEntity.AdminLoginRequest{
		RawAPIKey:       strings.TrimSpace(request.AdminAPIKey),
		MFAMethod:       iamEntity.MFAType(strings.TrimSpace(strings.ToLower(request.MFAMethod))),
		MFACode:         strings.TrimSpace(request.MFACode),
		DevicePublicKey: strings.TrimSpace(request.DevicePublicKey),
		HostnameHint:    hostnameHint,
		HostnameAlias:   hostnameAlias,
		ClientDeviceID:  clientDeviceIDHint,
	})
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument),
			errors.Is(err, iamTaxonomy.ErrAdminLoginInvalidCredential),
			errors.Is(err, iamTaxonomy.ErrAdminLoginMFAInvalid),
			errors.Is(err, iamTaxonomy.ErrAdminLoginDeviceRevoked),
			errors.Is(err, iamTaxonomy.ErrAdminLoginDeviceQuarantined),
			errors.Is(err, iamTaxonomy.ErrAdminLoginDeviceBindingFailed):
			logger.HandlerWarn(c, op, err, "admin login unauthorized")
			apires.RespondUnauthorized(c, "unauthorized")
			return
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
			return
		}
	}

	secure := c.Request.TLS != nil
	domain := strings.TrimSpace(h.cfg.App.PublicDomain)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.AdminAPITokenName,
		Value:    result.AdminAPIToken,
		Path:     "/admin",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.ExpiresAt,
		MaxAge:   int(time.Until(result.ExpiresAt).Seconds()),
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.AccessKeyName,
		Value:    result.AccessKey,
		Path:     "/admin",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.ExpiresAt,
		MaxAge:   int(time.Until(result.ExpiresAt).Seconds()),
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.AccessSecretName,
		Value:    result.AccessSecret,
		Path:     "/admin",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.ExpiresAt,
		MaxAge:   int(time.Until(result.ExpiresAt).Seconds()),
	})
	cdidExpires := time.Now().UTC().Add(365 * 24 * time.Hour)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.ClientDeviceIDName,
		Value:    result.ClientDeviceID,
		Path:     "/admin",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  cdidExpires,
		MaxAge:   int(time.Until(cdidExpires).Seconds()),
	})

	logger.HandlerInfo(c, op, "admin login successful")

	// set client device id header for device tracking
	c.Header(deviceHint.HeaderClientDeviceID, result.ClientDeviceID)

	apires.RespondSuccess(c, map[string]any{"ok": true}, "ok")
}

// Session godoc
// @Summary Admin session bootstrap
// @Description Xác nhận admin runtime session hiện tại có hợp lệ để frontend hydrate auth state.
// @Tags admin-auth
// @Produce json
// @Success 200 {object} map[string]interface{} "authenticated"
// @Failure 401 {object} map[string]interface{} "unauthorized"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/auth/session [get]
func (h *AdminAuthHandler) Session(c *gin.Context) {
	apires.RespondSuccess(c, map[string]any{"authenticated": true}, "ok")
}

// Refresh godoc
// @Summary Admin refresh session
// @Description Làm mới admin session bằng cookie runtime hiện có và cập nhật thời hạn phiên.
// @Tags admin-auth
// @Produce json
// @Success 200 {object} map[string]interface{} "ok"
// @Failure 401 {object} map[string]interface{} "unauthorized"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/auth/refresh [post]
func (h *AdminAuthHandler) Refresh(c *gin.Context) {
	const op = "iam.admin_auth.refresh"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	accessKey, _ := c.Cookie(cookie.AccessKeyName)
	var requestIP *string
	if ip := strings.TrimSpace(c.ClientIP()); ip != "" {
		requestIP = &ip
	}
	var userAgent *string
	if ua := strings.TrimSpace(c.Request.UserAgent()); ua != "" {
		userAgent = &ua
	}
	if value, ok := c.Get(constant.ContextKeyAdminAccessKey); ok {
		if accessKeyCtx, castOK := value.(string); castOK {
			accessKey = accessKeyCtx
		}
	}
	result, err := h.svc.RefreshAdminSession(ctx, strings.TrimSpace(accessKey), requestIP, userAgent)
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument):
			logger.HandlerWarn(c, op, err, "admin refresh unauthorized")
			apires.RespondUnauthorized(c, "unauthorized")
			return
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
			return
		}
	}

	secure := c.Request.TLS != nil
	domain := strings.TrimSpace(h.cfg.App.PublicDomain)
	maxAge := int(time.Until(result.ExpiresAt).Seconds())
	http.SetCookie(c.Writer,
		&http.Cookie{
			Name:     cookie.AdminAPITokenName,
			Value:    result.AdminAPIToken,
			Path:     "/admin",
			Domain:   domain,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			Expires:  result.ExpiresAt,
			MaxAge:   maxAge,
		})
	http.SetCookie(c.Writer,
		&http.Cookie{Name: cookie.AccessKeyName,
			Value:    result.AccessKey,
			Path:     "/admin",
			Domain:   domain,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			Expires:  result.ExpiresAt,
			MaxAge:   maxAge})
	http.SetCookie(c.Writer,
		&http.Cookie{Name: cookie.AccessSecretName,
			Value:    result.AccessSecret,
			Path:     "/admin",
			Domain:   domain,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			Expires:  result.ExpiresAt,
			MaxAge:   maxAge})

	// Gia hạn Cookie định danh thiết bị dài hạn (365 ngày) tương tự như luồng Login
	cdidExpires := time.Now().UTC().Add(365 * 24 * time.Hour)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.ClientDeviceIDName,
		Value:    result.ClientDeviceID,
		Path:     "/admin",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  cdidExpires,
		MaxAge:   int(time.Until(cdidExpires).Seconds()),
	})

	logger.HandlerInfo(c, op, "admin refresh successful")

	c.Header("X-Session-Expires-In", strconv.Itoa(maxAge))
	apires.RespondSuccess(c, nil, "ok")
}

// Logout godoc
// @Summary Admin logout
// @Description Xóa runtime session admin: clear cookies admin_api_token, access_key, access_secret và cleanup runtime secret trong Redis.
// @Tags admin-auth
// @Produce json
// @Success 204 {string} string "No Content"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/auth/logout [post]
func (h *AdminAuthHandler) Logout(c *gin.Context) {
	const op = "iam.admin_auth.logout"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	accessKey, _ := c.Cookie(cookie.AccessKeyName)
	if value, ok := c.Get(constant.ContextKeyAdminAccessKey); ok {
		if accessKeyCtx, castOK := value.(string); castOK {
			accessKey = accessKeyCtx
		}
	}
	trimmedAccessKey := strings.TrimSpace(accessKey)
	if trimmedAccessKey == "" {
		secure := c.Request.TLS != nil
		domain := strings.TrimSpace(h.cfg.App.PublicDomain)
		exp := time.Unix(0, 0)
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     cookie.AdminAPITokenName,
			Value:    "",
			Path:     "/admin",
			Domain:   domain,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			Expires:  exp,
			MaxAge:   -1,
		})
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     cookie.AccessKeyName,
			Value:    "",
			Path:     "/admin",
			Domain:   domain,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			Expires:  exp,
			MaxAge:   -1,
		})
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     cookie.AccessSecretName,
			Value:    "",
			Path:     "/admin",
			Domain:   domain,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			Expires:  exp,
			MaxAge:   -1,
		})
		c.Status(http.StatusNoContent)
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
	if err := h.svc.AdminLogout(ctx, trimmedAccessKey, requestIP, userAgent); err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	secure := c.Request.TLS != nil
	domain := strings.TrimSpace(h.cfg.App.PublicDomain)
	exp := time.Unix(0, 0)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.AdminAPITokenName,
		Value:    "",
		Path:     "/admin",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  exp,
		MaxAge:   -1,
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.AccessKeyName,
		Value:    "",
		Path:     "/admin",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  exp,
		MaxAge:   -1,
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.AccessSecretName,
		Value:    "",
		Path:     "/admin",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  exp,
		MaxAge:   -1,
	})

	logger.HandlerInfo(c, op, "admin logout successful")
	c.Status(http.StatusNoContent)
}

// RotateKey godoc
// @Summary Admin rotate API key (emergency)
// @Description Rotate admin API key and deliver plaintext key via Telegram internal channel.
// @Tags admin-auth
// @Produce json
// @Success 200 {object} map[string]interface{} "ok"
// @Failure 401 {object} map[string]interface{} "unauthorized"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/auth/rotate-key [post]
func (h *AdminAuthHandler) RotateKey(c *gin.Context) {
	const op = "iam.admin_auth.rotate_key"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := h.svc.RotateAdminAPIKeyEmergency(ctx); err != nil {
		if errors.Is(err, iamTaxonomy.ErrAdminRotationLockBusy) {
			logger.HandlerWarn(c, op, err, "rotation lock busy")
			apires.RespondUnauthorized(c, "unauthorized")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	apires.RespondSuccess(c, nil, "ok")
}
