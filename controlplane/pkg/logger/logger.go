package logger

import (
	"context"
	"os"
	"strings"
	"time"

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
	L().WithFields(fields).Error(err.Error())
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
	L().WithFields(fields).Info(message)
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
	if err != nil {
		fields["error"] = err.Error()
	}
	L().WithFields(fields).Warn(message)
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
	if err != nil {
		fields["error"] = err.Error()
		L().WithFields(fields).Error(err.Error())
	}
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
