package iamHandler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamReq "controlplane/internal/iam/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"
	cookie "controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

	// Thu thập các dấu vết thiết bị (Device Fingerprint) để phục vụ chính sách bảo mật Zero-Trust:
	// - hostnameHint & hostnameAlias: Nhận dạng tên thiết bị chạy ứng dụng client (vd: phucle-macbook-pro)

	const (
		headerDeviceHostname = "X-Device-Hostname"
		headerDeviceNameAlt  = "X-Device-Name"
		headerClientDeviceID = "X-Client-Device-Id"
	)

	hostnameHint := c.GetHeader(headerDeviceHostname)
	hostnameAlias := c.GetHeader(headerDeviceNameAlt)
	deviceName := resolveDeviceName(hostnameHint, hostnameAlias)

	// - clientDeviceIDHint: Đọc mã định danh duy nhất của thiết bị từ Custom Header,
	//   nếu không có thì fallback tìm trong Cookie của Web Browser để ràng buộc thiết bị vật lý với Admin Session.
	clientDeviceIDHintStr := c.GetHeader(headerClientDeviceID)
	if clientDeviceIDHintStr == "" {
		if cookieValue, _ := c.Cookie(cookie.ClientDeviceIDName); cookieValue != "" {
			clientDeviceIDHintStr = cookieValue
		}
	}
	clientDeviceIDHint, err := uuid.Parse(clientDeviceIDHintStr)
	if err != nil {
		clientDeviceIDHint = uuid.Nil
	}

	// call to service
	result, err := h.svc.AdminLogin(ctx, iamEntity.AdminLoginRequest{
		RawAPIKey:       strings.TrimSpace(request.AdminAPIKey),
		MFAMethod:       iamEntity.MFAType(strings.TrimSpace(strings.ToLower(request.MFAMethod))),
		MFACode:         strings.TrimSpace(request.MFACode),
		DevicePublicKey: strings.TrimSpace(request.DevicePublicKey),
		ZoneCode:        strings.TrimSpace(request.ZoneCode),
		DeviceName:      deviceName,
		ClientDeviceID:  clientDeviceIDHint,
	})
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument),
			errors.Is(err, iamTaxonomy.ErrInvalidCredential),
			errors.Is(err, iamTaxonomy.ErrMFAInvalid),
			errors.Is(err, iamTaxonomy.ErrDeviceRevoked),
			errors.Is(err, iamTaxonomy.ErrDeviceQuarantined),
			errors.Is(err, iamTaxonomy.ErrDeviceBindingFailed):
			logger.HandlerWarn(c, op, err, "admin login unauthorized")
			apires.RespondUnauthorized(c, "Admin login failed.")
			return
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "Internal Server Error")
			return
		}
	}

	secure := isSecureRequest(c)
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
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.ZoneCodeName,
		Value:    strings.TrimSpace(request.ZoneCode),
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
		Value:    result.ClientDeviceID.String(),
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
	c.Header("X-Client-Device-Id", result.ClientDeviceID.String())

	apires.RespondSuccess(c, nil, "Admin login successful")
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

	// Lấy zone_code từ query parameter trước
	zoneCode := strings.TrimSpace(c.Query("zone_code"))
	if zoneCode == "" {
		// Nếu query param rỗng, fallback thử đọc từ Cookie (do browser tự đính kèm qua credentials)
		if cookieVal, err := c.Cookie(cookie.ZoneCodeName); err == nil {
			zoneCode = strings.TrimSpace(cookieVal)
		}
	}

	// Nếu vẫn trống -> Trả lỗi 400 Bad Request
	if zoneCode == "" {
		logger.HandlerWarn(c, op, fmt.Errorf("missing zone_code query parameter and cookie"), "admin refresh rejected")
		apires.RespondBadRequest(c, "zone_code query parameter is required")
		return
	}

	logger.HandlerInfo(c, op, fmt.Sprintf("admin refresh session initiated for zone: %s", zoneCode))

	var requestIP *string
	if ip := strings.TrimSpace(c.ClientIP()); ip != "" {
		requestIP = &ip
	}
	var userAgent *string
	if ua := strings.TrimSpace(c.Request.UserAgent()); ua != "" {
		userAgent = &ua
	}
	result, err := h.svc.RefreshAdminSession(ctx, zoneCode, requestIP, userAgent)
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

	secure := isSecureRequest(c)
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
	http.SetCookie(c.Writer,
		&http.Cookie{Name: cookie.ZoneCodeName,
			Value:    zoneCode,
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
		Value:    result.ClientDeviceID.String(),
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
	if ident, ok := c.Request.Context().Value(constant.IdentityKey).(*constant.Identity); ok && ident != nil {
		if ident.AccessKey != "" {
			accessKey = ident.AccessKey
		}
	}
	trimmedAccessKey := strings.TrimSpace(accessKey)
	if trimmedAccessKey == "" {
		// Nhánh 1: Client không có session key (chưa đăng nhập hoặc cookie đã mất).
		// Không cần gọi backend thu hồi, chỉ cần xóa sạch cookie phía Client để đồng bộ trạng thái và trả về 204.
		secure := isSecureRequest(c)
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
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     cookie.ZoneCodeName,
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

	// Nhánh 2: Gọi backend thu hồi session thành công.
	// Tiến hành xóa cookie phía Client để hoàn tất quá trình đăng xuất.
	secure := isSecureRequest(c)
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
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.ZoneCodeName,
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
		if errors.Is(err, iamTaxonomy.ErrPreconditionFailed) {
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

func sanitizeHostname(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(cleaned))
	for _, r := range cleaned {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			builder.WriteRune(r)
		default:
		}
	}
	candidate := builder.String()
	if len(candidate) > 64 {
		candidate = candidate[:64]
	}
	if len(candidate) < 2 {
		return ""
	}
	return candidate
}

func resolveDeviceName(hostnameHeader, hostnameAlias string) string {
	if name := sanitizeHostname(hostnameHeader); name != "" {
		return name
	}
	if name := sanitizeHostname(hostnameAlias); name != "" {
		return name
	}
	return "unknown device"
}

// isSecureRequest checks if the request is secure (HTTPS) either directly or via reverse proxy.
func isSecureRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	if c.Request.TLS != nil {
		return true
	}
	proto := strings.ToLower(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")))
	return proto == "https"
}
