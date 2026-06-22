package iamHandler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	domainservice "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"
	cookie "controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

// RefreshTokenHandler tiếp nhận và điều hướng toàn bộ yêu cầu làm mới/gia hạn phiên
// của cả End-User và SRE Admin về tầng Service xử lý tương ứng.
type RefreshTokenHandler struct {
	refreshSvc domainservice.SessionRefreshService
	cfg        *config.Config
}

// NewRefreshTokenHandler tạo HTTP handler cho các refresh endpoints (Opaque Refresh & Admin Refresh).
// [COMMENT]: Luồng Trinity Refresh (Kiểu 1 - Sliding Session) đã được chuyển sang Rust ACL (ext_authz)
// xử lý transparent tại tầng Envoy Gateway, không cần HTTP endpoint riêng.
func NewRefreshTokenHandler(
	cfg *config.Config,
	refreshSvc domainservice.SessionRefreshService,
) *RefreshTokenHandler {
	return &RefreshTokenHandler{
		refreshSvc: refreshSvc,
		cfg:        cfg,
	}
}

// Refresh godoc
// @Summary Refresh session
// @Description Làm mới access token bằng refresh token cookie và rotate refresh token (Kiểu 2).
// @Tags auth
// @Produce json
// @Success 204 {string} string "No Content"
// @Failure 401 {object} map[string]interface{} "invalid session"
// @Failure 503 {object} map[string]interface{} "authentication temporarily unavailable"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/auth/refresh [post]
func (h *RefreshTokenHandler) Refresh(c *gin.Context) {
	const op = "iam.refresh_token.refresh"

	secure := isSecureRequest(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// [COMMENT]: Đọc refresh_token từ cookie
	rawRefreshToken, err := c.Cookie(cookie.RefreshTokenName)
	if err != nil || strings.TrimSpace(rawRefreshToken) == "" {
		// [COMMENT]: Nếu không tìm thấy cookie, dọn dẹp toàn bộ cookies hiện tại trên client để tránh trạng thái không đồng bộ
		h.clearUserCookies(c, secure)
		logger.HandlerWarn(c, op, iamTaxonomy.ErrInvalidSession, "refresh token missing")
		apires.RespondUnauthorized(c, "invalid session")
		return
	}

	// [COMMENT]: Gọi SessionRefreshService để xoay vòng refresh token và lấy bộ credentials mới
	result, err := h.refreshSvc.RefreshUserOpaque(ctx, strings.TrimSpace(rawRefreshToken))
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidSession):
			h.clearUserCookies(c, secure)
			logger.HandlerWarn(c, op, err, "refresh token invalid session")
			apires.RespondUnauthorized(c, "invalid session")
			return
		case errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable):
			logger.HandlerWarn(c, op, err, "refresh token authentication unavailable")
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			return
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
			return
		}
	}

	// [COMMENT]: Thiết lập cookie mới và trả về trạng thái 204 No Content
	h.setUserCookies(c, result, secure)
	c.Status(http.StatusNoContent)
}

// AdminRefresh godoc
// @Summary Admin refresh session
// @Description Làm mới admin session bằng cookie runtime hiện có và cập nhật thời hạn phiên.
// @Tags admin-auth
// @Produce json
// @Success 200 {object} map[string]interface{} "ok"
// @Failure 401 {object} map[string]interface{} "unauthorized"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /admin/auth/refresh [post]
func (h *RefreshTokenHandler) AdminRefresh(c *gin.Context) {
	const op = "iam.refresh_token.admin_refresh"

	// [COMMENT]: Khởi tạo context với timeout và tiêm tên operation vào context
	ctx := constant.WithOperation(c.Request.Context(), "admin_refresh")
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// [COMMENT]: Lấy zone_code từ query parameter hoặc fallback từ Cookie
	zoneCode := strings.TrimSpace(c.Query("zone_code"))
	if zoneCode == "" {
		if cookieVal, err := c.Cookie(cookie.ZoneCodeName); err == nil {
			zoneCode = strings.TrimSpace(cookieVal)
		}
	}
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

	// [COMMENT]: Gọi SessionRefreshService để xử lý Admin Trinity Refresh
	result, err := h.refreshSvc.RefreshAdminTrinity(ctx, zoneCode, requestIP, userAgent)
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

	// [COMMENT]: Thiết lập các cookies mới cho Admin
	http.SetCookie(c.Writer, &http.Cookie{
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
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.AccessKeyName,
		Value:    result.AccessKey,
		Path:     "/admin",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.ExpiresAt,
		MaxAge:   maxAge,
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
		MaxAge:   maxAge,
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.ZoneCodeName,
		Value:    zoneCode,
		Path:     "/admin",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.ExpiresAt,
		MaxAge:   maxAge,
	})

	// [COMMENT]: Gia hạn Cookie định danh thiết bị admin dài hạn (365 ngày)
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

	apires.RespondSuccess(c, nil, "ok")
}

// ======================================================================================================
// HELPER METHODS
// ======================================================================================================
func (h *RefreshTokenHandler) clearUserCookies(c *gin.Context, secure bool) {
	domain := strings.TrimSpace(h.cfg.App.PublicDomain)
	clearCookie := func(name string, httpOnly bool) {
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			Domain:   domain,
			HttpOnly: httpOnly,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
		})
	}
	clearCookie(cookie.AccessTokenName, true)
	clearCookie(cookie.RefreshTokenName, true)
	clearCookie(cookie.AccessKeyName, true)
	clearCookie(cookie.AccessSecretName, true)
}

func (h *RefreshTokenHandler) setUserCookies(c *gin.Context, result *iamEntity.RefreshTokenResult, secure bool) {
	domain := strings.TrimSpace(h.cfg.App.PublicDomain)
	setCookie := func(name, val string, expires time.Time, httpOnly bool) {
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     name,
			Value:    val,
			Path:     "/",
			Domain:   domain,
			HttpOnly: httpOnly,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			Expires:  expires,
			MaxAge:   int(time.Until(expires).Seconds()),
		})
	}
	setCookie(cookie.AccessTokenName, result.AccessToken, result.AccessExpiresAt, true)
	setCookie(cookie.RefreshTokenName, result.RefreshToken, result.RefreshExpiresAt, true)
	setCookie(cookie.AccessKeyName, result.AccessKey, result.AccessExpiresAt, true)
	setCookie(cookie.AccessSecretName, result.AccessSecret, result.AccessExpiresAt, true)
}
