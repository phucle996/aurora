package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

type xssiResponseWriter struct {
	gin.ResponseWriter
	wrotePrefix bool
}

func (w *xssiResponseWriter) Write(b []byte) (int, error) {
	contentType := w.Header().Get("Content-Type")
	if !w.wrotePrefix && strings.Contains(contentType, "application/json") {
		w.wrotePrefix = true
		_, _ = w.ResponseWriter.Write([]byte(")]}',\n"))
	}
	return w.ResponseWriter.Write(b)
}

func (w *xssiResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// AdminXSSI returns a Gin middleware that transparently prepends the standard
// JSON vulnerability prefix )]}',\n to all outgoing application/json responses.
func AdminXSSI() gin.HandlerFunc {
	return func(c *gin.Context) {
		w := &xssiResponseWriter{ResponseWriter: c.Writer}
		c.Writer = w
		c.Next()
	}
}
