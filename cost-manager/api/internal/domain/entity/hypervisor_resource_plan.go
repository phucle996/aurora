package entity

import (
	"time"

	"github.com/google/uuid"
)

// HypervisorResourcePlanRevision is the flat Cost-owned resource bundle that
// a VM pins at creation. It does not embed pricing schedules or Zone state.
type HypervisorResourcePlanRevision struct {
	PlanID         uuid.UUID
	RevisionID     uuid.UUID
	RevisionNumber int64
	Code           string
	DisplayName    string
	Description    string
	BillingModel   string
	CPUCores       int64
	MemoryMIB      int64
	BootDiskGIB    int64
	ContentSHA256  string
	EffectiveFrom  time.Time
	EffectiveTo    *time.Time
	State          string
	CreatedAt      time.Time
}

type CreateHypervisorResourcePlanCommand struct {
	PlanID        uuid.UUID
	RevisionID    uuid.UUID
	EventID       uuid.UUID
	Code          string
	DisplayName   string
	Description   string
	CPUCores      int64
	MemoryMIB     int64
	BootDiskGIB   int64
	EffectiveFrom time.Time
	ChangeReason  string
	CreatedBy     uuid.UUID
	ContentSHA256 string
	OutboxPayload []byte
}

type PublishHypervisorResourcePlanRevisionCommand struct {
	PlanID                 uuid.UUID
	RevisionID             uuid.UUID
	EventID                uuid.UUID
	ExpectedLatestRevision int64
	CPUCores               int64
	MemoryMIB              int64
	BootDiskGIB            int64
	EffectiveFrom          time.Time
	ChangeReason           string
	CreatedBy              uuid.UUID
	ContentSHA256          string
	OutboxPayload          []byte
}

type HypervisorResourcePlanListQuery struct {
	At    time.Time
	Limit int
}

type HypervisorResourcePlanOutboxRow struct {
	ID         uuid.UUID
	EventID    uuid.UUID
	Payload    []byte
	ClaimToken uuid.UUID
	RetryCount int
}

type HypervisorResourcePlanRelayPolicy struct {
	ReplicaAcks int
	DurableWait time.Duration
}

// Administrative resource-plan projections never serve as publish authority.
type HypervisorResourcePlanAdminQuery struct {
	After uuid.UUID
	Limit int
	At    time.Time
}
type HypervisorResourcePlanHistoryQuery struct {
	PlanID uuid.UUID
	Before int64
	Limit  int
	At     time.Time
}
type HypervisorResourcePlanAdminItem struct {
	PlanID                  uuid.UUID
	Code                    string
	DisplayName             string
	Description             string
	State                   string
	LatestRevisionNumber    int64
	EffectiveRevisionNumber int64
}
type HypervisorResourcePlanHistoryItem struct {
	PlanID         uuid.UUID
	RevisionID     uuid.UUID
	RevisionNumber int64
	CPUCores       int64
	MemoryMIB      int64
	BootDiskGIB    int64
	EffectiveFrom  time.Time
	EffectiveTo    time.Time
	State          string
	ChangeReason   string
	IsLatest       bool
	IsEffective    bool
}
