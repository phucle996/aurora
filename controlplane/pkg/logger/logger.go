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
	KeyActorID     = "actor_id"
	KeyTenantID    = "tenant_id"
	KeyWorkspaceID = "workspace_id"
	KeyRequestID   = "request_id" // Legacy carrier; it is no longer serialized when trace_id exists.
)

const (
	LogTypeAccess  = "access"
	LogTypeHandler = "handler"
	LogTypeSystem  = "system"
)

type Fields = logrus.Fields

var log *logrus.Logger

type resourceHook struct {
	serviceName    string
	serviceVersion string
	instanceID     string
}

func (h resourceHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h resourceHook) Fire(entry *logrus.Entry) error {
	entry.Data["service_name"] = h.serviceName
	entry.Data["service_version"] = h.serviceVersion
	entry.Data["service_instance_id"] = h.instanceID
	return nil
}

// AppInfo chứa thông tin định danh dịch vụ cho Logger (Name và Version)
type AppInfo struct {
	Name    string
	Version string
}

// [COMMENT]: InitLogger khởi tạo Logger với cấu hình tên và phiên bản dịch vụ nạp từ AppInfo tĩnh
func InitLogger(info AppInfo) {
	log = logrus.New()
	log.SetOutput(os.Stderr)
	log.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
	log.SetLevel(logrus.InfoLevel)
	serviceName := strings.TrimSpace(info.Name)
	if serviceName == "" {
		serviceName = "controlplane"
	}
	serviceVersion := strings.TrimSpace(info.Version)
	if serviceVersion == "" {
		serviceVersion = "dev"
	}
	instanceID, err := os.Hostname()
	if err != nil || strings.TrimSpace(instanceID) == "" {
		instanceID = "unknown"
	}
	log.AddHook(resourceHook{serviceName: serviceName, serviceVersion: serviceVersion, instanceID: instanceID})
}

// [COMMENT]: InitLoggerName hỗ trợ khởi tạo nhanh Logger khi chỉ truyền tên dịch vụ
func InitLoggerName(serviceName string) {
	InitLogger(AppInfo{Name: serviceName, Version: "dev"})
}

func L() *logrus.Logger {
	if log == nil {
		InitLogger(AppInfo{Name: "controlplane", Version: "dev"})
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

func actorID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, _ := c.Get(KeyActorID)
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

func spanID(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	spanCtx := trace.SpanContextFromContext(c.Request.Context())
	if spanCtx.IsValid() {
		return spanCtx.SpanID().String()
	}
	return ""
}

func appendCorrelationFields(fields logrus.Fields, ctx context.Context) bool {
	correlation, ok := CorrelationFromContext(ctx)
	if !ok {
		return false
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

func appendRequestContext(fields logrus.Fields, c *gin.Context) {
	if c == nil {
		return
	}
	if value := actorID(c); value != "" {
		fields["actor_id"] = value
	}
	if value, ok := c.Get(KeyTenantID); ok {
		if tenantID, valid := value.(string); valid && strings.TrimSpace(tenantID) != "" {
			fields["tenant_id"] = tenantID
		}
	}
	if value, ok := c.Get(KeyWorkspaceID); ok {
		if workspaceID, valid := value.(string); valid && strings.TrimSpace(workspaceID) != "" {
			fields["workspace_id"] = workspaceID
		}
	}
}

func appendContextTrace(fields logrus.Fields, ctx context.Context) {
	if ctx == nil {
		return
	}
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return
	}
	fields["trace_id"] = spanCtx.TraceID().String()
	fields["span_id"] = spanCtx.SpanID().String()
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
		"event_code":  "HTTP_ACCESS",
		"method":      method,
		"route":       route,
		"status_code": statusCode,
		"latency_ms":  latencyMs,
	}
	appendRequestContext(fields, c)
	if tID := traceID(c); tID != "" {
		fields["trace_id"] = tID
	}
	if sID := spanID(c); sID != "" {
		fields["span_id"] = sID
	}
	if code := strings.TrimSpace(errorCode); code != "" {
		fields["reason"] = code
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
		"event_code": "HANDLER_INFO",
		"op":         op,
	}
	appendRequestContext(fields, c)
	if tID := traceID(c); tID != "" {
		fields["trace_id"] = tID
	}
	if sID := spanID(c); sID != "" {
		fields["span_id"] = sID
	}
	if c != nil && c.Request != nil {
		appendCorrelationFields(fields, c.Request.Context())
	}
	L().WithFields(fields).Info(apperr.SanitizeLogText(message))
}

func HandlerWarn(c *gin.Context, op string, err error, message string) {
	fields := logrus.Fields{
		"event_code": "HANDLER_WARNING",
		"op":         op,
	}
	appendRequestContext(fields, c)
	if tID := traceID(c); tID != "" {
		fields["trace_id"] = tID
	}
	if sID := spanID(c); sID != "" {
		fields["span_id"] = sID
	}
	if c != nil && c.Request != nil {
		appendCorrelationFields(fields, c.Request.Context())
	}
	appendErrorFields(fields, err)
	L().WithFields(fields).Warn(apperr.SanitizeLogText(message))
}

func HandlerError(c *gin.Context, op string, err error) {
	fields := logrus.Fields{
		"event_code": "HANDLER_ERROR",
		"op":         op,
	}
	appendRequestContext(fields, c)
	if tID := traceID(c); tID != "" {
		fields["trace_id"] = tID
	}
	if sID := spanID(c); sID != "" {
		fields["span_id"] = sID
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
		"event_code": "HANDLER_INFO",
		"op":         op,
	}
	appendContextTrace(fields, ctx)
	appendCorrelationFields(fields, ctx)
	L().WithFields(fields).Info(apperr.SanitizeLogText(message))
}

// HandlerWarnCtx ghi log mức Warn cho các handler không có gin.Context (ví dụ NATS/gRPC)
func HandlerWarnCtx(ctx context.Context, op string, err error, message string) {
	fields := logrus.Fields{
		"event_code": "HANDLER_WARNING",
		"op":         op,
	}
	appendContextTrace(fields, ctx)
	appendCorrelationFields(fields, ctx)
	appendErrorFields(fields, err)
	L().WithFields(fields).Warn(apperr.SanitizeLogText(message))
}

// HandlerErrorCtx ghi log mức Error cho các handler không có gin.Context (ví dụ NATS/gRPC)
func HandlerErrorCtx(ctx context.Context, op string, err error) {
	fields := logrus.Fields{
		"event_code": "HANDLER_ERROR",
		"op":         op,
	}
	appendContextTrace(fields, ctx)
	appendCorrelationFields(fields, ctx)
	if err != nil {
		appendErrorFields(fields, err)
		L().WithFields(fields).Error("handler error")
	}
}

func SysInfo(op, message string) {
	L().WithFields(logrus.Fields{"event_code": "SYSTEM_INFO", "op": op}).Info(apperr.SanitizeLogText(message))
}

func SysWarn(op, message string) {
	L().WithFields(logrus.Fields{"event_code": "SYSTEM_WARNING", "op": op}).Warn(apperr.SanitizeLogText(message))
}

func SysError(op, message string) {
	L().WithFields(logrus.Fields{"event_code": "SYSTEM_ERROR", "op": op}).Error(apperr.SanitizeLogText(message))
}

func SysFatal(op, message string) {
	L().WithFields(logrus.Fields{"event_code": "SYSTEM_FATAL", "op": op}).Fatal(apperr.SanitizeLogText(message))
}

func SysInfoCtx(ctx context.Context, op, message string) {
	fields := logrus.Fields{"event_code": "SYSTEM_INFO", "op": op}
	appendContextTrace(fields, ctx)
	appendCorrelationFields(fields, ctx)
	L().WithFields(fields).Info(apperr.SanitizeLogText(message))
}

func SysWarnCtx(ctx context.Context, op, message string) {
	fields := logrus.Fields{"event_code": "SYSTEM_WARNING", "op": op}
	appendContextTrace(fields, ctx)
	appendCorrelationFields(fields, ctx)
	L().WithFields(fields).Warn(apperr.SanitizeLogText(message))
}

func SysErrorCtx(ctx context.Context, op, message string) {
	fields := logrus.Fields{"event_code": "SYSTEM_ERROR", "op": op}
	appendContextTrace(fields, ctx)
	appendCorrelationFields(fields, ctx)
	L().WithFields(fields).Error(apperr.SanitizeLogText(message))
}
