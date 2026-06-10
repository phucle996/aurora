package logger

import (
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

func InitLogger() {
	log = logrus.New()
	log.SetOutput(os.Stderr)
	log.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
	log.SetLevel(logrus.InfoLevel)
}

func L() *logrus.Logger {
	if log == nil {
		log = logrus.New()
		log.SetOutput(os.Stderr)
		log.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
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

	spanCtx := trace.SpanContextFromContext(c.Request.Context())
	if spanCtx.IsValid() {
		return spanCtx.TraceID().String()
	}
	return ""
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
	L().WithFields(fields).Info(message)
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
	appendAppErrorFields(fields, err)
	if err != nil {
		fields["error"] = err.Error()
	}
	L().WithFields(fields).Warn(message)
}

func HandlerWarnWithFields(c *gin.Context, op string, err error, message string, extra Fields) {
	fields := logrus.Fields{
		"log_type":   LogTypeHandler,
		"request_id": requestID(c),
		"user_id":    userID(c),
		"op":         op,
	}
	if tID := traceID(c); tID != "" {
		fields["trace_id"] = tID
	}
	appendAppErrorFields(fields, err)
	for key, value := range extra {
		fields[key] = value
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	L().WithFields(fields).Warn(message)
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
	appendAppErrorFields(fields, err)
	if err != nil {
		fields["error"] = err.Error()
	}
	L().WithFields(fields).Error("handler error")
}

func appendAppErrorFields(fields logrus.Fields, err error) {
	appErrFields := apperr.LogFields(err)
	for key, value := range appErrFields {
		fields[key] = value
	}
}

func HandlerErrorWithFields(c *gin.Context, op string, err error, extra Fields) {
	fields := logrus.Fields{
		"log_type":   LogTypeHandler,
		"request_id": requestID(c),
		"user_id":    userID(c),
		"op":         op,
	}
	if tID := traceID(c); tID != "" {
		fields["trace_id"] = tID
	}
	appendAppErrorFields(fields, err)
	for key, value := range extra {
		fields[key] = value
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	L().WithFields(fields).Error("handler error")
}

func mergeSystemFields(op string, fields Fields, err error) logrus.Fields {
	merged := logrus.Fields{"log_type": LogTypeSystem, "op": op}
	for k, v := range fields {
		merged[k] = v
	}
	if err != nil {
		merged["error"] = err.Error()
	}
	return merged
}

func SysInfoFields(op, message string, fields Fields) {
	L().WithFields(mergeSystemFields(op, fields, nil)).Info(message)
}

func SysDebugFields(op, message string, fields Fields) {
	L().WithFields(mergeSystemFields(op, fields, nil)).Debug(message)
}

func SysWarnFields(op, message string, err error, fields Fields) {
	L().WithFields(mergeSystemFields(op, fields, err)).Warn(message)
}

func SysErrorFields(op, message string, err error, fields Fields) {
	L().WithFields(mergeSystemFields(op, fields, err)).Error(message)
}

func SysInfo(op, message string) {
	L().WithFields(logrus.Fields{"log_type": LogTypeSystem, "op": op}).Info(message)
}

func SysWarn(op, message string) {
	L().WithFields(logrus.Fields{"log_type": LogTypeSystem, "op": op}).Warn(message)
}

func SysError(op, message string) {
	L().WithFields(logrus.Fields{"log_type": LogTypeSystem, "op": op}).Error(message)
}

func SysFatal(op, message string) {
	L().WithFields(logrus.Fields{"log_type": LogTypeSystem, "op": op}).Fatal(message)
}
