package middleware_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"controlplane/internal/http/middleware"
	"controlplane/internal/observability"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

func TestAccessLogUsesWorkflowCorrelationForSuccessfulRequest(t *testing.T) {
	logger.InitLogger("controlplane-test")
	var output bytes.Buffer
	logger.L().SetOutput(&output)
	t.Cleanup(func() { logger.L().SetOutput(os.Stderr) })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.RequestID(), middleware.AccessLog())
	recorder := observability.NewNoopMetrics().ForModule("iam")
	router.GET("/api/v1/me/profile", func(c *gin.Context) {
		ctx := pkgcontext.WithOperation(c.Request.Context(), "iam.users.get_my_profile")
		recorder.ObserveWorkflow(ctx, observability.ResultSuccess, observability.ReasonNone, time.Millisecond)
		c.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me/profile", nil)
	router.ServeHTTP(response, request)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode access log: %v; output=%s", err, output.String())
	}
	for key, want := range map[string]string{
		"module": "iam",
		"op":     "iam.users.get_my_profile",
		"result": "success",
		"reason": "none",
		"route":  "/api/v1/me/profile",
	} {
		if record[key] != want {
			t.Fatalf("field %s = %#v, want %q", key, record[key], want)
		}
	}
}
