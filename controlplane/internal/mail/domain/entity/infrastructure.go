package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

// MailInfrastructure là current read-only operational snapshot cho trang Admin của một Zone.
// Entity không chứa management endpoint, credential hoặc customer mail payload.
type MailInfrastructure struct {
	ZoneID             uuid.UUID
	DesiredState       bool
	ActualState        string
	EventID            *uuid.UUID
	ReportGeneration   uint64
	ReportSequence     uint64
	ServiceState       string
	Capacity           uint32
	PendingItems       uint64
	InFlightBatches    uint64
	ProbeNodeID        string
	DataplaneNodes     []MailDataplaneNode
	StalwartNodes      []MailStalwartNode
	InventoryTruncated bool
	ErrorCode          string
	ReportedAt         *time.Time
	ExpiresAt          *time.Time
	Fresh              bool
}

type MailDataplaneNode struct {
	NodeID              string    `json:"node_id"`
	BootID              uuid.UUID `json:"boot_id"`
	State               string    `json:"state"`
	Capacity            uint32    `json:"capacity"`
	PendingItems        uint64    `json:"pending_items"`
	InFlightBatches     uint64    `json:"in_flight_batches"`
	ActiveConsumerSlots uint32    `json:"active_consumer_slots"`
	JMAPReachable       bool      `json:"jmap_reachable"`
	LastProbeAtUnixMS   int64     `json:"last_probe_at_unix_ms"`
	ObservedAtUnixMS    int64     `json:"observed_at_unix_ms"`
	ErrorCode           string    `json:"error_code"`
}

type MailStalwartNode struct {
	NodeID            uint64 `json:"node_id"`
	Hostname          string `json:"hostname"`
	State             string `json:"state"`
	LastRenewalUnixMS int64  `json:"last_renewal_unix_ms"`
}
