package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"controlplane/pkg/apires"
	cookie "controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

const (
	headerOrigin  = "Origin"
	headerReferer = "Referer"
)

var cookieAuthNames = map[string]struct{}{
	cookie.AccessTokenName:  {},
	cookie.RefreshTokenName: {},
	"apitoken":      {},
}

// CookieOriginGuard enforces Origin/Referer checks for unsafe requests
// when the request is authenticated by cookies.
func CookieOriginGuard(allowedOrigins []string) gin.HandlerFunc {
	normalizedAllowed := normalizeAllowedOrigins(allowedOrigins)

	return func(c *gin.Context) {
		if c == nil || c.Request == nil {
			c.Next()
			return
		}

		if !isUnsafeMethod(c.Request.Method) {
			c.Next()
			return
		}

		if !usesCookieAuth(c.Request) {
			c.Next()
			return
		}

		origin, ok := extractRequestOrigin(c.Request)
		if !ok {
			logger.HandlerWarn(c, "security.origin", nil, "missing origin/referer for cookie-auth unsafe request")
			apires.RespondForbidden(c, "forbidden")
			c.Abort()
			return
		}

		if !isAllowedOrigin(origin, normalizedAllowed, c.Request.Host) {
			logger.HandlerWarn(c, "security.origin", nil, "origin not allowed for cookie-auth unsafe request")
			apires.RespondForbidden(c, "forbidden")
			c.Abort()
			return
		}

		c.Next()
	}
}

func usesCookieAuth(r *http.Request) bool {
	if r == nil {
		return false
	}

	if bearer, ok := r.Header["Authorization"]; ok {
		for _, raw := range bearer {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "bearer ") {
				return false
			}
		}
	}

	cookies := r.Cookies()
	for _, ck := range cookies {
		if ck == nil {
			continue
		}
		if _, ok := cookieAuthNames[strings.TrimSpace(ck.Name)]; ok {
			return true
		}
	}

	return false
}

func isUnsafeMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func normalizeAllowedOrigins(origins []string) map[string]struct{} {
	out := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		normalized, ok := normalizeOrigin(origin)
		if !ok {
			continue
		}
		out[normalized] = struct{}{}
	}
	return out
}

func extractRequestOrigin(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}

	if origin, ok := normalizeOrigin(strings.TrimSpace(r.Header.Get(headerOrigin))); ok {
		return origin, true
	}

	referer := strings.TrimSpace(r.Header.Get(headerReferer))
	if referer == "" {
		return "", false
	}

	u, err := url.Parse(referer)
	if err != nil || strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return "", false
	}
	return normalizeOrigin(u.Scheme + "://" + u.Host)
}

func normalizeOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	host := strings.ToLower(strings.TrimSpace(u.Host))
	if (scheme != "http" && scheme != "https") || host == "" {
		return "", false
	}

	return scheme + "://" + host, true
}

func isAllowedOrigin(origin string, allowed map[string]struct{}, reqHost string) bool {
	if origin == "" {
		return false
	}

	if _, ok := allowed[origin]; ok {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	return strings.EqualFold(strings.TrimSpace(u.Host), strings.TrimSpace(reqHost))
}
