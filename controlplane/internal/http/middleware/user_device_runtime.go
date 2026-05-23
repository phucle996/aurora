package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	iamCache "controlplane/internal/iam/cache"
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
)

// UserDeviceCookieScope mô tả phạm vi cookie phía user-flow để middleware
// có thể clear đúng Domain/Secure khi reject.
type UserDeviceCookieScope struct {
	Domain string
	Path   string
}

// UserDeviceRuntimeVerifier ràng buộc user request với cặp device fragment
// (cookie device_id + cookie device_secret) và jti hiện hành đang lưu trong
// Redis runtime cache (key by tracking_id).
//
// SoT của presence và device-binding là Redis user device runtime, không phải
// refresh token. Middleware này phải chạy SAU Access() để lấy được claims.
//
// Hành vi:
//   - thiếu cookie device_id/device_secret hoặc thiếu tracking_id/jti trong claims
//     -> 401 unauthorized.
//   - mismatch fragment / hash secret / jti (ngoài graceWindow) -> 401, xoá runtime,
//     clear cookie device_id, device_secret.
//   - mismatch -> không reveal lý do cụ thể cho client, chỉ trả unauthorized.
func RequireUserDeviceRuntime(cache iamCache.UserDeviceRuntimeCache, graceWindow time.Duration, cookieScope UserDeviceCookieScope) gin.HandlerFunc {
	if graceWindow <= 0 {
		graceWindow = 10 * time.Second
	}
	cookiePath := strings.TrimSpace(cookieScope.Path)
	if cookiePath == "" {
		cookiePath = "/"
	}
	cookieDomain := strings.TrimSpace(cookieScope.Domain)
	return func(c *gin.Context) {
		if cache == nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		deviceIDCookie := strings.TrimSpace(readCookie(c, constant.DeviceIDName))
		deviceSecret := strings.TrimSpace(readCookie(c, constant.DeviceSecretName))
		trackingID := strings.TrimSpace(GetTrackingID(c))
		jti := strings.TrimSpace(readJTI(c))

		if deviceIDCookie == "" || deviceSecret == "" || trackingID == "" || jti == "" {
			rejectUserDeviceRuntime(c, cookieDomain, cookiePath)
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
		defer cancel()

		record, err := cache.GetDeviceRuntime(ctx, trackingID)
		if err != nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}
		if record == nil || !iamCache.MatchUserDeviceRuntime(record, deviceIDCookie, deviceSecret, jti, graceWindow) {
			if record != nil {
				_ = cache.DeleteDeviceRuntime(ctx, trackingID)
			}
			rejectUserDeviceRuntime(c, cookieDomain, cookiePath)
			return
		}

		if record.TrackedDeviceRef != "" {
			c.Set(CtxKeyTrackedDeviceID, record.TrackedDeviceRef)
		}

		c.Next()
	}
}

func readCookie(c *gin.Context, name string) string {
	value, err := c.Cookie(name)
	if err != nil {
		return ""
	}
	return value
}

func readJTI(c *gin.Context) string {
	v, ok := c.Get(CtxKeyJTI)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func rejectUserDeviceRuntime(c *gin.Context, cookieDomain, cookiePath string) {
	clearUserDeviceCookies(c, cookieDomain, cookiePath)
	apires.RespondUnauthorized(c, "unauthorized")
	c.Abort()
}

func clearUserDeviceCookies(c *gin.Context, cookieDomain, cookiePath string) {
	secure := c.Request.TLS != nil
	exp := time.Unix(0, 0)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     constant.DeviceIDName,
		Value:    "",
		Path:     cookiePath,
		Domain:   cookieDomain,
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  exp,
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     constant.DeviceSecretName,
		Value:    "",
		Path:     cookiePath,
		Domain:   cookieDomain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  exp,
	})
}
