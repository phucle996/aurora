package middleware

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/observability"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
)

const (
	traceparentHeader = "traceparent"
	// HeaderXRequestID is the standard header for Request ID.
	headerXRequestID = "X-Request-ID"
)

func OTelTraceContext(obs *observability.OTel) gin.HandlerFunc {
	return func(c *gin.Context) {
		if obs == nil {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		if strings.TrimSpace(c.GetHeader(traceparentHeader)) != "" {
			ctx = obs.Extract(ctx, c.Request.Header)
		}

		spanName := fmt.Sprintf("%s %s", c.Request.Method, requestRoute(c))
		ctx, span := obs.StartServerSpan(ctx, spanName)
		defer span.End()

		// Keep both request and response aligned to the effective trace context.
		obs.Inject(ctx, c.Request.Header)
		if tp := strings.TrimSpace(c.Request.Header.Get(traceparentHeader)); tp != "" {
			c.Header(traceparentHeader, tp)
		}

		c.Request = c.Request.WithContext(ctx)
		c.Next()

		span.SetAttributes(
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.route", requestRoute(c)),
			attribute.Int("http.status_code", c.Writer.Status()),
		)
		if len(c.Errors) > 0 {
			span.RecordError(c.Errors.Last())
		}
	}
}

func PrometheusHTTPMetrics(obs *observability.Prometheus) gin.HandlerFunc {
	return func(c *gin.Context) {
		if obs == nil {
			c.Next()
			return
		}

		start := time.Now()
		obs.IncInFlight()
		defer obs.DecInFlight()

		c.Next()

		obs.ObserveRequest(
			c.Request.Method,
			requestRoute(c),
			strconv.Itoa(c.Writer.Status()),
			time.Since(start),
		)
	}
}

func PrometheusMetricsEndpoint(obs *observability.Prometheus) gin.HandlerFunc {
	if obs == nil {
		return func(c *gin.Context) {
			c.AbortWithStatus(http.StatusServiceUnavailable)
		}
	}
	return gin.WrapH(obs.HTTPHandler())
}

func requestRoute(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return "/"
	}

	if fullPath := strings.TrimSpace(c.FullPath()); fullPath != "" {
		return fullPath
	}

	path := strings.TrimSpace(c.Request.URL.Path)
	if path == "" {
		return "/"
	}

	return path
}

func newRequestID() string {
	buf := make([]byte, 16)
	if _, err := cryptorand.Read(buf); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// RequestID generates a unique request ID for every incoming request,
// injects it into the gin.Context for logging, and attaches it to
// the HTTP response header.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := newRequestID()

		// Inject into gin.Context using the key expected by pkg/logger.
		c.Set(logger.KeyRequestID, reqID)

		// Set the header in the response so the client gets it too.
		c.Header(headerXRequestID, reqID)

		c.Next()
	}
}
