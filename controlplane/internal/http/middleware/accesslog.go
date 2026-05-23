package middleware

import (
	"net"
	"strings"
	"time"

	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

// AccessLog emits a structured access log after the request finishes.

// r.Use(middleware.AccessLog())
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		route := requestPath(c)
		if route == "" {
			route = c.FullPath()
		}
		if route == "" {
			route = "unknown"
		}

		statusCode := c.Writer.Status()
		latencyMs := float64(time.Since(start)) / float64(time.Millisecond)
		errorCode := ""

		if len(c.Errors) > 0 {
			errorCode = "request_error"
		}

		if statusCode >= 500 && errorCode == "" {
			errorCode = "http_error"
		}

		logger.AccessLog(
			c,
			route,
			errorCode,
			requestMethod(c),
			route,
			statusCode,
			latencyMs,
			requestIP(c),
		)
	}
}

func requestPath(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return ""
	}

	return strings.TrimSpace(c.Request.URL.Path)
}

func requestMethod(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}

	return strings.TrimSpace(c.Request.Method)
}

func requestIP(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}

	if ip := strings.TrimSpace(c.ClientIP()); ip != "" {
		return ip
	}

	addr := strings.TrimSpace(c.Request.RemoteAddr)
	if addr == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return strings.TrimSpace(host)
	}

	return addr
}
