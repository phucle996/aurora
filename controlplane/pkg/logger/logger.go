package logger

import (
	"context"
	"os"
	"strings"
	"time"

	"controlplane/pkg/apperr"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
)

const (
	KeyRequestID = "request_id"
	KeyUserID    = "user_id"
)

const (
	LogTypeAccess  = "access"
	LogTypeHandler = "handler"
	LogTypeSystem  = "system"
)

type Fields = logrus.Fields

var log *logrus.Logger

type resourceHook struct {
	serviceName string
	instanceID  string
}

func (h resourceHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h resourceHook) Fire(entry *logrus.Entry) error {
	entry.Data["service_name"] = h.serviceName
	entry.Data["service_instance_id"] = h.instanceID
	entry.Data["aurora_component"] = "controlplane"
	return nil
}

func InitLogger(serviceName string) {
	log = logrus.New()
	log.SetOutput(os.Stderr)
	log.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
	log.SetLevel(logrus.InfoLevel)
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		serviceName = "controlplane"
	}
	instanceID, err := os.Hostname()
	if err != nil || strings.TrimSpace(instanceID) == "" {
		instanceID = "unknown"
	}
	log.AddHook(resourceHook{serviceName: serviceName, instanceID: instanceID})
}

func L() *logrus.Logger {
	if log == nil {
		InitLogger("controlplane")
	}
	return log
}

func requestID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, _ := c.Get(KeyRequestID)
	s, _ := v.(string)
	return s
}

func userID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, _ := c.Get(KeyUserID)
	s, _ := v.(string)
	return s
}

func traceID(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	spanCtx := trace.SpanContextFromContext(c.Request.Context())
	if spanCtx.IsValid() {
		return spanCtx.TraceID().String()
	}
	return ""
}

func appendCorrelationFields(fields logrus.Fields, ctx context.Context) bool {
	correlation, ok := CorrelationFromContext(ctx)
	if !ok {
		return false
	}
	if correlation.Module != "" {
		fields["module"] = correlation.Module
	}
	if correlation.Operation != "" {
		fields["op"] = correlation.Operation
	}
	if correlation.Observed {
		fields["result"] = correlation.Result
		fields["reason"] = correlation.Reason
	}
	return correlation.Observed
}

func appendErrorFields(fields logrus.Fields, err error) {
	if err == nil {
		return
	}
	fields["error_kind"] = apperr.SanitizeLogText(err.Error())
	for key, value := range apperr.LogFields(err) {
		fields[key] = value
	}
}

func AccessLog(c *gin.Context, op, errorCode, method, route string, statusCode int, latencyMs float64, clientIP string) {
	fields := logrus.Fields{
		"log_type":    LogTypeAccess,
		"request_id":  requestID(c),
		"method":      method,
		"route":       route,
		"status_code": statusCode,
		"latency_ms":  latencyMs,
		"client_ip":   clientIP,
	}
	if tID := traceID(c); tID != "" {
		fields["trace_id"] = tID
	}
	if code := strings.TrimSpace(errorCode); code != "" {
		fields["error_code"] = code
	}
	if opValue := strings.TrimSpace(op); opValue != "" && opValue != strings.TrimSpace(route) {
		fields["op"] = opValue
	}
	observed := false
	if c != nil && c.Request != nil {
		observed = appendCorrelationFields(fields, c.Request.Context())
	}
	if !observed {
		switch {
		case statusCode >= 500:
			fields["result"] = "failure"
		case statusCode >= 400:
			fields["result"] = "rejected"
		default:
			fields["result"] = "success"
		}
	}
	L().WithFields(fields).Info("access")
}

func HandlerInfo(c *gin.Context, op, message string) {
	fields := logrus.Fields{
		"log_type":   LogTypeHandler,
		"request_id": requestID(c),
		"user_id":    userID(c),
		"op":         op,
	}
	if tID := traceID(c); tID != "" {
		fields["trace_id"] = tID
	}
	if c != nil && c.Request != nil {
		appendCorrelationFields(fields, c.Request.Context())
	}
	L().WithFields(fields).Info(apperr.SanitizeLogText(message))
}

func HandlerWarn(c *gin.Context, op string, err error, message string) {
	fields := logrus.Fields{
		"log_type":   LogTypeHandler,
		"request_id": requestID(c),
		"user_id":    userID(c),
		"op":         op,
	}
	if tID := traceID(c); tID != "" {
		fields["trace_id"] = tID
	}
	if c != nil && c.Request != nil {
		appendCorrelationFields(fields, c.Request.Context())
	}
	appendErrorFields(fields, err)
	L().WithFields(fields).Warn(apperr.SanitizeLogText(message))
}

func HandlerError(c *gin.Context, op string, err error) {
	fields := logrus.Fields{
		"log_type":   LogTypeHandler,
		"request_id": requestID(c),
		"user_id":    userID(c),
		"op":         op,
	}
	if tID := traceID(c); tID != "" {
		fields["trace_id"] = tID
	}
	if c != nil && c.Request != nil {
		appendCorrelationFields(fields, c.Request.Context())
	}
	appendErrorFields(fields, err)
	L().WithFields(fields).Error("handler error")
}

// HandlerInfoCtx ghi log mức Info cho các handler không có gin.Context (ví dụ NATS/gRPC)
func HandlerInfoCtx(ctx context.Context, op, message string) {
	fields := logrus.Fields{
		"log_type": LogTypeHandler,
		"op":       op,
	}
	if ctx != nil {
		spanCtx := trace.SpanContextFromContext(ctx)
		if spanCtx.IsValid() {
			fields["trace_id"] = spanCtx.TraceID().String()
		}
	}
	appendCorrelationFields(fields, ctx)
	L().WithFields(fields).Info(apperr.SanitizeLogText(message))
}

// HandlerWarnCtx ghi log mức Warn cho các handler không có gin.Context (ví dụ NATS/gRPC)
func HandlerWarnCtx(ctx context.Context, op string, err error, message string) {
	fields := logrus.Fields{
		"log_type": LogTypeHandler,
		"op":       op,
	}
	if ctx != nil {
		spanCtx := trace.SpanContextFromContext(ctx)
		if spanCtx.IsValid() {
			fields["trace_id"] = spanCtx.TraceID().String()
		}
	}
	appendCorrelationFields(fields, ctx)
	appendErrorFields(fields, err)
	L().WithFields(fields).Warn(apperr.SanitizeLogText(message))
}

// HandlerErrorCtx ghi log mức Error cho các handler không có gin.Context (ví dụ NATS/gRPC)
func HandlerErrorCtx(ctx context.Context, op string, err error) {
	fields := logrus.Fields{
		"log_type": LogTypeHandler,
		"op":       op,
	}
	if ctx != nil {
		spanCtx := trace.SpanContextFromContext(ctx)
		if spanCtx.IsValid() {
			fields["trace_id"] = spanCtx.TraceID().String()
		}
	}
	appendCorrelationFields(fields, ctx)
	if err != nil {
		appendErrorFields(fields, err)
		L().WithFields(fields).Error("handler error")
	}
}

func SysInfo(op, message string) {
	L().WithFields(logrus.Fields{"log_type": LogTypeSystem, "op": op}).Info(apperr.SanitizeLogText(message))
}

func SysWarn(op, message string) {
	L().WithFields(logrus.Fields{"log_type": LogTypeSystem, "op": op}).Warn(apperr.SanitizeLogText(message))
}

func SysError(op, message string) {
	L().WithFields(logrus.Fields{"log_type": LogTypeSystem, "op": op}).Error(apperr.SanitizeLogText(message))
}

func SysFatal(op, message string) {
	L().WithFields(logrus.Fields{"log_type": LogTypeSystem, "op": op}).Fatal(apperr.SanitizeLogText(message))
}

func SysInfoCtx(ctx context.Context, op, message string) {
	fields := logrus.Fields{"log_type": LogTypeSystem, "op": op}
	if ctx != nil {
		if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
			fields["trace_id"] = spanCtx.TraceID().String()
		}
	}
	appendCorrelationFields(fields, ctx)
	L().WithFields(fields).Info(apperr.SanitizeLogText(message))
}

func SysWarnCtx(ctx context.Context, op, message string) {
	fields := logrus.Fields{"log_type": LogTypeSystem, "op": op}
	if ctx != nil {
		if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
			fields["trace_id"] = spanCtx.TraceID().String()
		}
	}
	appendCorrelationFields(fields, ctx)
	L().WithFields(fields).Warn(apperr.SanitizeLogText(message))
}

func SysErrorCtx(ctx context.Context, op, message string) {
	fields := logrus.Fields{"log_type": LogTypeSystem, "op": op}
	if ctx != nil {
		if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
			fields["trace_id"] = spanCtx.TraceID().String()
		}
	}
	appendCorrelationFields(fields, ctx)
	L().WithFields(fields).Error(apperr.SanitizeLogText(message))
}
