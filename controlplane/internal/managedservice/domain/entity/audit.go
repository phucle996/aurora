package entity

import (
	"time"

	"github.com/google/uuid"
)

type AuditEventView struct {
	ID              uuid.UUID
	ActorSubject    string
	CriticalProofID *uuid.UUID
	Action          string
	RecordKind      string
	RecordID        uuid.UUID
	RecordVersion   int64
	Outcome         string
	ErrorCode       *string
	OccurredAt      time.Time
}

type ListAuditEvents struct{ Limit int }
