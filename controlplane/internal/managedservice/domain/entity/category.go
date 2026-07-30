package entity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type CategoryView struct {
	ID              uuid.UUID
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

type CreateCategory struct {
	ID              uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	Code            string
	Name            string
	Description     string
	NameI18n        json.RawMessage
	DescriptionI18n json.RawMessage
	IconKey         string
	AfterHash       []byte
}

type ListCategories struct{ Limit int }
type GetCategory struct{ CategoryID uuid.UUID }

type UpdateCategory struct {
	CategoryID      uuid.UUID
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

type RetireCategory struct {
	CategoryID      uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	ProofID         uuid.UUID
	ExpectedVersion int64
}
