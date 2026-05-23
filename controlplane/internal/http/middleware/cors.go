package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS returns a middleware that handles Cross-Origin Resource Sharing.
func CORS(allowedOrigins []string) gin.HandlerFunc {
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		origins[strings.ToLower(strings.TrimSpace(o))] = struct{}{}
	}

	return func(c *gin.Context) {
		origin := strings.ToLower(strings.TrimSpace(c.GetHeader("Origin")))
		if origin == "" {
			c.Next()
			return
		}

		// Check if origin is allowed
		_, allowed := origins[origin]
		if !allowed {
			// Also allow if it matches the request host (same-origin)
			if strings.HasSuffix(origin, c.Request.Host) {
				allowed = true
			}
		}

		if allowed {
			c.Header("Access-Control-Allow-Origin", c.GetHeader("Origin"))
			c.Header("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE, PATCH")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-Request-ID, X-Device-ID")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Max-Age", "86400")
		}

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
