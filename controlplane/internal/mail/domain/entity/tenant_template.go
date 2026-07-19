package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

type TenantTemplate struct {
	ActorUserID         uuid.UUID
	TenantID            uuid.UUID
	ZoneID              uuid.UUID
	ID                  string
	WorkspaceID         *uuid.UUID
	Name                string
	CurrentVersion      uint64
	TemplateRevision    uint64
	Status              TemplateStatus
	IdempotencyKey      string
	CreateRequestSHA256 []byte
	ArchivedAt          *time.Time
	CreatedBy           *uuid.UUID
	UpdatedBy           *uuid.UUID
	UpdatedAt           time.Time
	ExpectedRevision    uint64
	BeforeVersion       uint64
	AfterID             string
	Limit               uint32
	TemplateID          string
	Version             uint64
	SubjectTemplate     string
	HTMLTemplate        string
	ContentSHA256       []byte
	CreatedAt           time.Time
	VersionCreatedBy    *uuid.UUID
	VersionCreatedAt    time.Time
}
