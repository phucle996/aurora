package policyEntity

import "time"

type PolicySet struct {
	Version     string
	UpdatedAt   time.Time
	Source      string
	ChecksumSHA string
	Policies    map[string]interface{}
	Runtime     RuntimePolicies
}

// RuntimePolicies carries compiled typed runtime variables for middleware callers.
type RuntimePolicies struct {
	AdminCIDR CompiledAdminCIDRPolicy
}

// CompiledAdminCIDRPolicy is typed runtime variable parsed from YAML contract.
// Middleware reads this variable as input source without changing enforcement logic.
type CompiledAdminCIDRPolicy struct {
	Enabled   bool
	Mode      string
	Allowlist []string
}

// PolicySourceMeta describes low-cost source metadata used for change detection.
// Metadata is used to skip expensive parse path when source state is unchanged.
type PolicySourceMeta struct {
	Path    string
	Version string
	Size    int64
}

// PolicyChangedEvent carries propagation metadata only.
// Payload never contains full policy body to keep bus traffic small and safe.
type PolicyChangedEvent struct {
	Version          string
	Checksum         string
	SourceType       string
	EmittedAtUnixSec int64
	EmitterInstance  string
}
