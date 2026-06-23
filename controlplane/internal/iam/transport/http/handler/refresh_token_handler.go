package iamHandler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"controlplane/internal/config"
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
