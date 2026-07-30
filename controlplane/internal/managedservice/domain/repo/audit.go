package repo

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
)

type AuditRepository interface {
	ListAuditEvents(context.Context, *entity.ListAuditEvents) ([]entity.AuditEventView, error)
}
