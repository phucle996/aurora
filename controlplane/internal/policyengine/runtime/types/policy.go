package policytypes

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
	RateLimit CompiledRateLimitPolicyGroup
}

// CompiledAdminCIDRPolicy is typed runtime variable parsed from YAML contract.
// Middleware reads this variable as input source without changing enforcement logic.
type CompiledAdminCIDRPolicy struct {
	Enabled   bool
	Mode      string
	Allowlist []string
}

// CompiledRateLimitPolicyGroup groups rate-limit runtime policies by function scope.
type CompiledRateLimitPolicyGroup struct {
	PreAuth       CompiledRateLimitPreAuthPolicy
	PostAuth      CompiledRateLimitPostAuthPolicy
	Observability CompiledRateLimitObservabilityPolicy
	Behavior      CompiledRateLimitBehaviorPolicy
}

type CompiledRateLimitPreAuthPolicy struct {
	GlobalInstant CompiledRateLimitGlobalInstantPolicy
	IP            CompiledRateLimitBucketPolicy
}

type CompiledRateLimitPostAuthPolicy struct {
	IPDevice CompiledRateLimitBucketPolicy
}

type CompiledRateLimitGlobalInstantPolicy struct {
	MaxInflight       int64
	QueueLimit        int64
	RetryAfterSeconds int64
}

type CompiledRateLimitBucketPolicy struct {
	Capacity      int64
	Refill        int64
	PeriodSeconds int64
}

type CompiledRateLimitObservabilityPolicy struct {
	SamplingPercent CompiledRateLimitSamplingPercentPolicy
}

type CompiledRateLimitSamplingPercentPolicy struct {
	Throttle           int
	TemporaryIsolation int
	Block              int
	Error              int
}

type CompiledRateLimitBehaviorPolicy struct {
	RetryAfterFallbackSeconds int64
	FailOpen                  bool
	BypassRoutePatterns       []string
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
