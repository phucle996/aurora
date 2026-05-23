package iamHandler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"controlplane/internal/config"
	"controlplane/internal/http/middleware"
	domainservice "controlplane/internal/iam/domain/service"
	apires "controlplane/pkg/apires"
	cookie "controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

// LogoutHandler handles POST /api/v1/auth/logout.
//
// Handler chỉ làm validation cookie/claims và set lại cookie. Mọi mutation
// (DEL runtime, revoke refresh) đều ở service. Đảm bảo handler không leak
// runtime detail của Redis hay DB.
type LogoutHandler struct {
	cfg     *config.Config
	authSvc domainservice.AuthService
}

func NewLogoutHandler(cfg *config.Config, authSvc domainservice.AuthService) *LogoutHandler {
	return &LogoutHandler{cfg: cfg, authSvc: authSvc}
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
func (h *LogoutHandler) Logout(c *gin.Context) {
	const op = "iam.auth.logout"

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID := strings.TrimSpace(middleware.GetUserID(c))
	trackingID := strings.TrimSpace(middleware.GetTrackingID(c))

	// Cho phép logout dù thiếu tracking_id (best-effort) miễn là user_id còn hợp lệ.
	if userID == "" {
		clearAuthCookies(c, h.cfg)
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}

	if err := h.authSvc.Logout(ctx, userID, trackingID); err != nil {
		logger.HandlerError(c, op, err)
		// Vẫn clear cookies để session phía client không kẹt.
		clearAuthCookies(c, h.cfg)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	clearAuthCookies(c, h.cfg)
	logger.HandlerInfo(c, op, "user logout successful")
	c.Status(http.StatusNoContent)
}

func clearAuthCookies(c *gin.Context, cfg *config.Config) {
	domain := strings.TrimSpace(cfg.App.PublicDomain)
	secure := c.Request.TLS != nil
	exp := time.Unix(0, 0)
	for _, cookieDef := range []struct {
		name     string
		httpOnly bool
	}{
		{cookie.AccessTokenName, true},
		{cookie.RefreshTokenName, true},
		{cookie.DeviceIDName, false},
		{cookie.DeviceSecretName, true},
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
}
