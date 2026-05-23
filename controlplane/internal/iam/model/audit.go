package iamModel

import (
	"time"

	"github.com/google/uuid"

	"controlplane/internal/iam/domain/entity"
)

type AuditEvent struct {
	ID          string
	ActorUserID *uuid.UUID
	TenantID    *uuid.UUID
	WorkspaceID *uuid.UUID
	Event       string
	Severity    string
	IPAddress   *string
	UserAgent   *string
	CreatedAt   time.Time
}

type AdminActionAudit struct {
	ID            uuid.UUID
	Action        string
	ResourceType  string
	ResourceID    *string
	Status        string
	RequestIP     *string
	RequestPath   string
	RequestMethod string
	ErrorCode     *string
	Metadata      map[string]any
	CreatedAt     time.Time
}

func AuditEventEntityToModel(input iamEntity.AuditEvent) AuditEvent {
	return AuditEvent{
		ID:          input.ID,
		ActorUserID: input.ActorUserID,
		TenantID:    input.TenantID,
		WorkspaceID: input.WorkspaceID,
		Event:       input.Event,
		Severity:    string(input.Severity),
		IPAddress:   input.IPAddress,
		UserAgent:   input.UserAgent,
		CreatedAt:   input.CreatedAt}
}
func AuditEventModelToEntity(input AuditEvent) iamEntity.AuditEvent {
	return iamEntity.AuditEvent{
		ID:          input.ID,
		ActorUserID: input.ActorUserID,
		TenantID:    input.TenantID,
		WorkspaceID: input.WorkspaceID,
		Event:       input.Event,
		Severity:    iamEntity.AuditSeverity(input.Severity),
		IPAddress:   input.IPAddress,
		UserAgent:   input.UserAgent,
		CreatedAt:   input.CreatedAt}
}

func AdminActionAuditEntityToModel(input iamEntity.AdminActionAudit) AdminActionAudit {
	return AdminActionAudit{
		ID:            input.ID,
		Action:        input.Action,
		ResourceType:  input.ResourceType,
		ResourceID:    input.ResourceID,
		Status:        string(input.Status),
		RequestIP:     input.RequestIP,
		RequestPath:   input.RequestPath,
		RequestMethod: input.RequestMethod,
		ErrorCode:     input.ErrorCode,
		Metadata:      input.Metadata,
		CreatedAt:     input.CreatedAt,
	}
}

func AdminActionAuditModelToEntity(input AdminActionAudit) iamEntity.AdminActionAudit {
	return iamEntity.AdminActionAudit{
		ID:            input.ID,
		Action:        input.Action,
		ResourceType:  input.ResourceType,
		ResourceID:    input.ResourceID,
		Status:        iamEntity.AdminActionAuditStatus(input.Status),
		RequestIP:     input.RequestIP,
		RequestPath:   input.RequestPath,
		RequestMethod: input.RequestMethod,
		ErrorCode:     input.ErrorCode,
		Metadata:      input.Metadata,
		CreatedAt:     input.CreatedAt,
	}
}
