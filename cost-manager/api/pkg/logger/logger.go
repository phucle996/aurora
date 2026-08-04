package logger

import (
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
)

const (
	KeyActorID     = "actor_id"
	KeyTenantID    = "tenant_id"
	KeyWorkspaceID = "workspace_id"
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

func (h resourceHook) Levels() []logrus.Level { return logrus.AllLevels }

func (h resourceHook) Fire(entry *logrus.Entry) error {
	entry.Data["service_name"] = h.serviceName
	entry.Data["service_version"] = h.serviceVersion
	entry.Data["service_instance_id"] = h.instanceID
	return nil
}

func InitLogger() {
	log = logrus.New()
	log.SetOutput(os.Stderr)
	log.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
	log.SetLevel(logrus.InfoLevel)
	serviceName := strings.TrimSpace(os.Getenv("APP_NAME"))
	if serviceName == "" {
		serviceName = "cost-manager-api"
	}
	serviceVersion := strings.TrimSpace(os.Getenv("APP_VERSION"))
	if serviceVersion == "" {
		serviceVersion = "dev"
	}
	instanceID, err := os.Hostname()
	if err != nil || strings.TrimSpace(instanceID) == "" {
		instanceID = "unknown"
	}
	log.AddHook(resourceHook{serviceName: serviceName, serviceVersion: serviceVersion, instanceID: instanceID})
}

func L() *logrus.Logger {
	if log == nil {
		InitLogger()
	}
	return log
}

func actorID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, _ := c.Get(KeyActorID)
	s, _ := v.(string)
	return s
}

func appendContextFields(fields logrus.Fields, c *gin.Context) {
	if c == nil {
		return
	}
	if value := actorID(c); value != "" {
		fields["actor_id"] = value
	}
	for key, field := range map[string]string{
		KeyTenantID:    "tenant_id",
		KeyWorkspaceID: "workspace_id",
	} {
		if value, ok := c.Get(key); ok {
			if text, valid := value.(string); valid && strings.TrimSpace(text) != "" {
				fields[field] = text
			}
		}
	}
}

func appendTrace(fields logrus.Fields, c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	if spanContext := trace.SpanContextFromContext(c.Request.Context()); spanContext.IsValid() {
		fields["trace_id"] = spanContext.TraceID().String()
		fields["span_id"] = spanContext.SpanID().String()
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
	appendContextFields(fields, c)
	appendTrace(fields, c)
	if code := strings.TrimSpace(errorCode); code != "" {
		fields["reason"] = code
	}
	if opValue := strings.TrimSpace(op); opValue != "" && opValue != strings.TrimSpace(route) {
		fields["op"] = opValue
	}
	L().WithFields(fields).Info("access")
}

func HandlerInfo(c *gin.Context, op, message string) {
	fields := logrus.Fields{
		"event_code": "HANDLER_INFO",
		"op":         op,
	}
	appendContextFields(fields, c)
	appendTrace(fields, c)
	L().WithFields(fields).Info(message)
}

func HandlerWarn(c *gin.Context, op string, err error, message string) {
	fields := logrus.Fields{
		"event_code": "HANDLER_WARNING",
		"op":         op,
	}
	appendContextFields(fields, c)
	appendTrace(fields, c)
	if err != nil {
		fields["error"] = err.Error()
	}
	L().WithFields(fields).Warn(message)
}

func HandlerError(c *gin.Context, op string, err error) {
	fields := logrus.Fields{
		"event_code": "HANDLER_ERROR",
		"op":         op,
	}
	appendContextFields(fields, c)
	appendTrace(fields, c)
	if err != nil {
		L().WithFields(fields).Error(err.Error())
	}
}

func SysInfo(op, message string) {
	L().WithFields(logrus.Fields{"event_code": "SYSTEM_INFO", "op": op}).Info(message)
}

func SysWarn(op, message string) {
	L().WithFields(logrus.Fields{"event_code": "SYSTEM_WARNING", "op": op}).Warn(message)
}

func SysError(op, message string) {
	L().WithFields(logrus.Fields{"event_code": "SYSTEM_ERROR", "op": op}).Error(message)
}

func SysFatal(op, message string) {
	L().WithFields(logrus.Fields{"event_code": "SYSTEM_FATAL", "op": op}).Fatal(message)
}
