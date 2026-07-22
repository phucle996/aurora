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
	Code             string
	Name             string
	CurrentVersion   uint64
	TemplateRevision uint64
	NextVersion      uint64
	NextRevision     uint64
	OperationID      uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ExpectedRevision uint64
	BeforeVersion    uint64
	AfterID          string
	Limit            uint32
	TemplateID       string
	Version          uint64
	SubjectTemplate  string
	HTMLTemplate     string
	ContentSHA256    []byte
	VersionCreatedAt time.Time
}
