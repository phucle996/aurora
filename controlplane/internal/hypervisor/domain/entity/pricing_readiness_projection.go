package hypervisorEntity

import "time"

type PricingReadinessProjectionCommand struct {
	SchemaVersion int
	Ready         bool
	Missing       []string
	ObservedAt    time.Time
	ValidUntil    time.Time
	Fingerprint   string
}

type PricingReadinessProjection struct {
	SchemaVersion int
	Ready         bool
	Missing       []string
	ObservedAt    time.Time
	ValidUntil    time.Time
	Fingerprint   string
}
