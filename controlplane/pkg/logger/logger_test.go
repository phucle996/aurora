package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"controlplane/pkg/apperr"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
)

func TestHandlerErrorEmitsBoundedCorrelationAndSanitizedCause(t *testing.T) {
	previous := log
	t.Cleanup(func() { log = previous })
	InitLogger("controlplane-test")
	var output bytes.Buffer
	L().SetOutput(&output)

	ctx := WithCorrelation(context.Background())
	SetCorrelationOperation(ctx, "iam.auth.verify_credentials")
	SetCorrelationOutcome(ctx, "iam", "iam.auth.verify_credentials", "failure", "unavailable")
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/login", nil).WithContext(ctx)
	c.Set(KeyRequestID, "request-1")
	HandlerError(c, "iam.auth.verify_credentials", apperr.Wrap(
		errors.New("authentication unavailable"),
		errors.New("password=must-never-be-logged"),
		"dependency_error",
	))

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode log: %v; output=%s", err, output.String())
	}
	for key, want := range map[string]string{
		"module":      "iam",
		"op":          "iam.auth.verify_credentials",
		"result":      "failure",
		"reason":      "unavailable",
		"trace_id":    traceID.String(),
		"request_id":  "request-1",
		"error_class": "dependency_error",
	} {
		if record[key] != want {
			t.Fatalf("field %s = %#v, want %q", key, record[key], want)
		}
	}
	if record["error_cause"] != "[redacted_sensitive_cause]" {
		t.Fatalf("error_cause = %#v", record["error_cause"])
	}
	if strings.Contains(output.String(), "must-never-be-logged") {
		t.Fatal("sensitive cause leaked into structured log")
	}
	if record["service_name"] != "controlplane-test" || record["service_instance_id"] == "" {
		t.Fatalf("resource identity missing: %#v", record)
	}
}
