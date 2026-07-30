package entity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type DefinitionView struct {
	ID              uuid.UUID
	CategoryID      uuid.UUID
	Code            string
	Name            string
	Description     string
	NameI18n        json.RawMessage
	DescriptionI18n json.RawMessage
	IconKey         string
	State           string
	RowVersion      int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CreateDefinition struct {
	ID              uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	CategoryID      uuid.UUID
	Code            string
	Name            string
	Description     string
	NameI18n        json.RawMessage
	DescriptionI18n json.RawMessage
	IconKey         string
	AfterHash       []byte
}

type ListDefinitions struct {
	CategoryID uuid.UUID
	Limit      int
}
type GetDefinition struct{ DefinitionID uuid.UUID }

type UpdateDefinition struct {
	DefinitionID    uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	ExpectedVersion int64
	Name            string
	Description     string
	NameI18n        json.RawMessage
	DescriptionI18n json.RawMessage
	IconKey         string
	AfterHash       []byte
}

type RetireDefinition struct {
	DefinitionID    uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	ProofID         uuid.UUID
	ExpectedVersion int64
}
