package middleware

import (
	"controlplane/internal/ratelimit"
	"controlplane/pkg/apires"
	"net"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimit applies a token-bucket limit to the current request.
//
//	middleware.RateLimit(m.RateLimiter, "auth_register", 5, 5, time.Minute)
func RateLimit(limiter *ratelimit.Bucket, name string, capacity, refill int64, period time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if limiter == nil || name == "" || capacity <= 0 || refill <= 0 || period <= 0 {
			c.Next()
			return
		}

		key := ratelimit.Key("", name, clientIdentity(c))
		if key == "" {
			c.Next()
			return
		}

		res, err := limiter.Allow(
			c.Request.Context(),
			key,
			ratelimit.Rate{
				Capacity: capacity,
				Refill:   refill,
				Period:   period,
			},
			1,
		)

		for k, v := range ratelimit.RateLimitHeaders(res) {
			c.Writer.Header().Set(k, v)
		}

		if err != nil && !res.Allowed {
			apires.RespondServiceUnavailable(c, "rate limit temporarily unavailable")
			c.Abort()
			return
		}

		if !res.Allowed {
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		c.Next()
	}
}

func clientIdentity(c *gin.Context) string {
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
