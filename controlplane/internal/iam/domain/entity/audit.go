package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

type AuditSeverity string

const (
	AuditSeverityInfo     AuditSeverity = "info"
	AuditSeverityWarning  AuditSeverity = "warning"
	AuditSeverityCritical AuditSeverity = "critical"
)

type AuditEvent struct {
	ID          string
	ActorUserID *uuid.UUID
	TenantID    *uuid.UUID
	WorkspaceID *uuid.UUID
	Event       string
	Severity    AuditSeverity
	IPAddress   *string
	UserAgent   *string
	CreatedAt   time.Time
}

type AdminActionAuditStatus string

const (
	AdminActionAuditStatusSuccess AdminActionAuditStatus = "success"
	AdminActionAuditStatusFailed  AdminActionAuditStatus = "failed"
)

type AdminActionAudit struct {
	ID           uuid.UUID
	Action       string
	ResourceType string
	ResourceID   *string
	Status       AdminActionAuditStatus
	RequestIP    *string
	RequestPath  string
	RequestMethod string
	ErrorCode    *string
	Metadata     map[string]any
	CreatedAt    time.Time
}
