package service

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
)

type AuditService interface {
	ListAuditEvents(context.Context, *entity.ListAuditEvents) ([]entity.AuditEventView, error)
}
