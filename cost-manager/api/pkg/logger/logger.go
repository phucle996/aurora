package logger

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
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
		InitLogger()
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
	L().WithFields(fields).Info(message)
}

func HandlerWarn(c *gin.Context, op string, err error, message string) {
	fields := logrus.Fields{
		"log_type":   LogTypeHandler,
		"request_id": requestID(c),
		"user_id":    userID(c),
		"op":         op,
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
	if err != nil {
		L().WithFields(fields).Error(err.Error())
	}
}

func HandlerInfoCtx(ctx context.Context, op, message string) {
	fields := logrus.Fields{
		"log_type": LogTypeHandler,
		"op":       op,
	}
	L().WithFields(fields).Info(message)
}

func HandlerWarnCtx(ctx context.Context, op string, err error, message string) {
	fields := logrus.Fields{
		"log_type": LogTypeHandler,
		"op":       op,
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	L().WithFields(fields).Warn(message)
}

func HandlerErrorCtx(ctx context.Context, op string, err error) {
	fields := logrus.Fields{
		"log_type": LogTypeHandler,
		"op":       op,
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
