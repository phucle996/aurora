package entity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type VersionView struct {
	ID              uuid.UUID
	DefinitionID    uuid.UUID
	Code            string
	DisplayVersion  string
	NameI18n        json.RawMessage
	DescriptionI18n json.RawMessage
	IconKey         string
	State           string
	RowVersion      int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CreateVersion struct {
	ID              uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	DefinitionID    uuid.UUID
	Code            string
	DisplayVersion  string
	NameI18n        json.RawMessage
	DescriptionI18n json.RawMessage
	IconKey         string
	AfterHash       []byte
}

type ListVersions struct {
	DefinitionID uuid.UUID
	Limit        int
}
type GetVersion struct{ VersionID uuid.UUID }

type UpdateVersion struct {
	VersionID       uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	ExpectedVersion int64
	DisplayVersion  string
	NameI18n        json.RawMessage
	DescriptionI18n json.RawMessage
	IconKey         string
	AfterHash       []byte
}

type DeprecateVersion struct {
	VersionID       uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	ProofID         uuid.UUID
	ExpectedVersion int64
}

type RetireVersion struct {
	VersionID       uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	ProofID         uuid.UUID
	ExpectedVersion int64
}
