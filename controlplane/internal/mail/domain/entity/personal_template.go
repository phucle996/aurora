package mailEntity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type PersonalTemplate struct {
	ActorUserID      uuid.UUID
	ZoneID           uuid.UUID
	ID               string
	WorkspaceID      *uuid.UUID
	Scope            TemplateScope
	Name             string
	CurrentVersion   uint64
	TemplateRevision uint64
	Status           TemplateStatus
	// [COMMENT]: Idempotency create thuộc HTTP command, không thuộc immutable content version.
	IdempotencyKey      string
	CreateRequestSHA256 []byte
	ArchivedAt          *time.Time
	CreatedBy           *uuid.UUID
	CreatedAt           time.Time
	UpdatedAt           time.Time
	ExpectedRevision    uint64
	BeforeVersion       uint64
	AfterID             string
	Limit               uint32
	TemplateID          string
	Version             uint64
	SubjectTemplate     string
	TextTemplate        string
	HTMLTemplate        string
	VariableSchemaJSON  json.RawMessage
	ContentSHA256       []byte
	VersionCreatedBy    *uuid.UUID
	VersionCreatedAt    time.Time
}
