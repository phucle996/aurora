package entity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type DraftView struct {
	ID                       uuid.UUID
	BlueprintID              uuid.UUID
	Revision                 int64
	State                    string
	TemplateYAML             string
	TemplateBundleSHA256     []byte
	ContractVersion          string
	ContractSHA256           []byte
	ComponentContract        json.RawMessage
	InputSchema              json.RawMessage
	UISchema                 json.RawMessage
	SafeObservedOutputSchema json.RawMessage
	ZoneSelector             json.RawMessage
	CapabilityRequirement    json.RawMessage
	RowVersion               int64
	ValidatedRowVersion      *int64
	ValidatedAt              *time.Time
	CreatedAt                time.Time
	PublishedAt              *time.Time
	RetiredAt                *time.Time
}

type CreateDraft struct {
	ID                       uuid.UUID
	AuditID                  uuid.UUID
	Actor                    string
	ProofID                  uuid.UUID
	BlueprintID              uuid.UUID
	TemplateYAML             string
	TemplateBundleSHA256     []byte
	ContractVersion          string
	ContractSHA256           []byte
	ComponentContract        json.RawMessage
	ComponentContractSHA256  []byte
	InputSchema              json.RawMessage
	InputSchemaSHA256        []byte
	UISchema                 json.RawMessage
	UISchemaSHA256           []byte
	SafeObservedOutputSchema json.RawMessage
	SafeOutputSHA256         []byte
	ZoneSelector             json.RawMessage
	ZoneSelectorSHA256       []byte
	CapabilityRequirement    json.RawMessage
	CapabilitySHA256         []byte
}

type GetDraft struct{ DraftID uuid.UUID }

type ListRevisions struct {
	BlueprintID uuid.UUID
	Limit       int
}

type PatchDraft struct {
	DraftID                  uuid.UUID
	AuditID                  uuid.UUID
	Actor                    string
	ProofID                  uuid.UUID
	ExpectedVersion          int64
	TemplateYAML             string
	TemplateBundleSHA256     []byte
	ContractVersion          string
	ContractSHA256           []byte
	ComponentContract        json.RawMessage
	ComponentContractSHA256  []byte
	InputSchema              json.RawMessage
	InputSchemaSHA256        []byte
	UISchema                 json.RawMessage
	UISchemaSHA256           []byte
	SafeObservedOutputSchema json.RawMessage
	SafeOutputSHA256         []byte
	ZoneSelector             json.RawMessage
	ZoneSelectorSHA256       []byte
	CapabilityRequirement    json.RawMessage
	CapabilitySHA256         []byte
}

type ValidateDraft struct {
	DraftID              uuid.UUID
	AuditID              uuid.UUID
	Actor                string
	ProofID              uuid.UUID
	ExpectedVersion      int64
	TemplateBundleSHA256 []byte
	ContractSHA256       []byte
	ValidationContract   string
}

type PublishDraft struct {
	DraftID              uuid.UUID
	AuditID              uuid.UUID
	Actor                string
	ProofID              uuid.UUID
	ExpectedVersion      int64
	ExpectedBundleSHA256 []byte
	ExpectedContractHash []byte
}

type RetireRevision struct {
	RevisionID      uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	ProofID         uuid.UUID
	ExpectedVersion int64
}

type DeleteDraft struct {
	DraftID         uuid.UUID
	AuditID         uuid.UUID
	Actor           string
	ProofID         uuid.UUID
	ExpectedVersion int64
}
