package entity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type BlueprintView struct {
	ID                  uuid.UUID
	VersionID           uuid.UUID
	Code                string
	Name                string
	NameI18n            json.RawMessage
	DescriptionI18n     json.RawMessage
	IconKey             string
	State               string
	RowVersion          int64
	PublishedRevisionID *uuid.UUID
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type CreateBlueprint struct {
	ID              uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	ProofID         uuid.UUID
	VersionID       uuid.UUID
	Code            string
	Name            string
	NameI18n        json.RawMessage
	DescriptionI18n json.RawMessage
	IconKey         string
	AfterHash       []byte
}

type GetBlueprint struct{ BlueprintID uuid.UUID }

type GetBlueprintByVersion struct{ VersionID uuid.UUID }

type DeleteBlueprint struct {
	BlueprintID     uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	ProofID         uuid.UUID
	ExpectedVersion int64
}
