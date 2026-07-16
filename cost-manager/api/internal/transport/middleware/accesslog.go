package middleware

import (
	"time"

	"cost-manager/api/pkg/logger"
	"github.com/gin-gonic/gin"
)

// AccessLog records access logs after the HTTP request is processed
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := float64(time.Since(start).Nanoseconds()) / 1e6
		errorCode := ""
		if len(c.Errors) > 0 {
			errorCode = "request_error"
		} else if c.Writer.Status() >= 500 {
			errorCode = "http_error"
		}
		logger.AccessLog(c, c.HandlerName(), errorCode, c.Request.Method, c.Request.URL.Path, c.Writer.Status(), latency, c.ClientIP())
	}
}
