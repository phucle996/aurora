package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

type PersonalTemplate struct {
	ActorUserID      uuid.UUID
	ZoneID           uuid.UUID
	ID               string
	WorkspaceID      *uuid.UUID
	Name             string
	CurrentVersion   uint64
	TemplateRevision uint64
	Status           TemplateStatus
	// [COMMENT]: Idempotency create thuộc HTTP command, không thuộc immutable content version.
	IdempotencyKey      string
	CreateRequestSHA256 []byte
	ArchivedAt          *time.Time
	CreatedAt           time.Time
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
	VersionCreatedAt    time.Time
}
