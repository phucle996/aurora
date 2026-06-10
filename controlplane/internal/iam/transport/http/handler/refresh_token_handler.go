package iamHandler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"controlplane/internal/config"
	domainservice "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	apires "controlplane/pkg/apires"
	cookie "controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

type RefreshTokenHandler struct {
	refreshTokenSvc domainservice.RefreshTokenService
	cfg             *config.Config
}

// NewRefreshTokenHandler tạo HTTP handler cho refresh token endpoint.
func NewRefreshTokenHandler(
	cfg *config.Config,
	refreshTokenSvc domainservice.RefreshTokenService,
) *RefreshTokenHandler {
	return &RefreshTokenHandler{
		refreshTokenSvc: refreshTokenSvc,
		cfg:             cfg,
	}
}

// Refresh godoc
// @Summary Refresh session
// @Description Làm mới access token bằng refresh token cookie và rotate refresh token.
// @Tags auth
// @Produce json
// @Success 204 {string} string "No Content"
// @Failure 401 {object} map[string]interface{} "invalid session"
// @Failure 503 {object} map[string]interface{} "authentication temporarily unavailable"
// @Failure 500 {object} map[string]interface{} "internal_error"
// @Router /api/v1/auth/refresh [post]
func (h *RefreshTokenHandler) Refresh(c *gin.Context) {
	const op = "iam.refresh_token.refresh"

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	rawRefreshToken, err := c.Cookie(cookie.RefreshTokenName)
	if err != nil || strings.TrimSpace(rawRefreshToken) == "" {
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     cookie.AccessTokenName,
			Value:    "",
			Path:     "/",
			Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
			HttpOnly: true,
			Secure:   c.Request.TLS != nil,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
		})
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     cookie.RefreshTokenName,
			Value:    "",
			Path:     "/",
			Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
			HttpOnly: true,
			Secure:   c.Request.TLS != nil,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
		})
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     cookie.AccessKeyName,
			Value:    "",
			Path:     "/",
			Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
			HttpOnly: false,
			Secure:   c.Request.TLS != nil,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
		})
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     cookie.AccessSecretName,
			Value:    "",
			Path:     "/",
			Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
			HttpOnly: true,
			Secure:   c.Request.TLS != nil,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
		})
		logger.HandlerWarn(c, op, iamTaxonomy.ErrInvalidSession, "refresh token missing")
		apires.RespondUnauthorized(c, "invalid session")
		return
	}

	result, err := h.refreshTokenSvc.Refresh(ctx, strings.TrimSpace(rawRefreshToken))
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrInvalidSession):
			http.SetCookie(c.Writer, &http.Cookie{
				Name:     cookie.AccessTokenName,
				Value:    "",
				Path:     "/",
				Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
				HttpOnly: true,
				Secure:   c.Request.TLS != nil,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   -1,
				Expires:  time.Unix(0, 0),
			})
			http.SetCookie(c.Writer, &http.Cookie{
				Name:     cookie.RefreshTokenName,
				Value:    "",
				Path:     "/",
				Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
				HttpOnly: true,
				Secure:   c.Request.TLS != nil,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   -1,
				Expires:  time.Unix(0, 0),
			})
			http.SetCookie(c.Writer, &http.Cookie{
				Name:     cookie.AccessKeyName,
				Value:    "",
				Path:     "/",
				Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
				HttpOnly: false,
				Secure:   c.Request.TLS != nil,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   -1,
				Expires:  time.Unix(0, 0),
			})
			http.SetCookie(c.Writer, &http.Cookie{
				Name:     cookie.AccessSecretName,
				Value:    "",
				Path:     "/",
				Domain:   strings.TrimSpace(h.cfg.App.PublicDomain),
				HttpOnly: true,
				Secure:   c.Request.TLS != nil,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   -1,
				Expires:  time.Unix(0, 0),
			})
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

	c.Status(http.StatusNoContent)
}
