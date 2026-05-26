package configyaml

// File này định nghĩa typed schema để parse `policies.yaml` runtime.
//
// Ví dụ YAML:
//
//	version: v1
//	policies:
//	  admin_cidr:
//	    enabled: true
//	    mode: enforce
//	    allowlist:
//	      - 127.0.0.1/32
//	      - 10.0.0.0/8
//
// Mapping YAML -> struct:
// - `version` -> PoliciesFile.Version
// - `policies` -> PoliciesFile.Policies
// - `policies.admin_cidr` -> PoliciesRuntimeRoot.AdminCIDR
// - `policies.admin_cidr.enabled` -> AdminCIDRPolicy.Enabled
// - `policies.admin_cidr.mode` -> AdminCIDRPolicy.Mode
// - `policies.admin_cidr.allowlist` -> AdminCIDRPolicy.Allowlist

// PoliciesFile is typed runtime YAML root model.
type PoliciesFile struct {
	Version  string              `yaml:"version"`
	Policies PoliciesRuntimeRoot `yaml:"policies"`
}

// PoliciesRuntimeRoot groups runtime policy sections.
type PoliciesRuntimeRoot struct {
	AdminCIDR  AdminCIDRPolicy  `yaml:"admin_cidr"`
	RateLimit  RateLimitPolicy  `yaml:"rate_limit"`
}

// AdminCIDRPolicy maps YAML keys for admin CIDR runtime policy.
type AdminCIDRPolicy struct {
	Enabled   bool     `yaml:"enabled"`
	Mode      string   `yaml:"mode"`
	Allowlist []string `yaml:"allowlist"`
}

type RateLimitPolicy struct {
	PreAuth       RateLimitPreAuthPolicy       `yaml:"preauth"`
	PostAuth      RateLimitPostAuthPolicy      `yaml:"postauth"`
	Observability RateLimitObservabilityPolicy `yaml:"observability"`
	Behavior      RateLimitBehaviorPolicy      `yaml:"behavior"`
}

type RateLimitPreAuthPolicy struct {
	GlobalInstant RateLimitGlobalInstantPolicy `yaml:"global_instant"`
	IP            RateLimitBucketPolicy        `yaml:"ip"`
}

type RateLimitPostAuthPolicy struct {
	IPDevice RateLimitBucketPolicy `yaml:"ip_device"`
}

type RateLimitGlobalInstantPolicy struct {
	MaxInflight       int64 `yaml:"max_inflight"`
	QueueLimit        int64 `yaml:"queue_limit"`
	RetryAfterSeconds int64 `yaml:"retry_after_seconds"`
}

type RateLimitBucketPolicy struct {
	Capacity      int64 `yaml:"capacity"`
	Refill        int64 `yaml:"refill"`
	PeriodSeconds int64 `yaml:"period_seconds"`
}

type RateLimitObservabilityPolicy struct {
	SamplingPercent RateLimitSamplingPercentPolicy `yaml:"sampling_percent"`
}

type RateLimitSamplingPercentPolicy struct {
	Throttle          int `yaml:"throttle"`
	TemporaryIsolation int `yaml:"temporary_isolation"`
	Block             int `yaml:"block"`
	Error             int `yaml:"error"`
}

type RateLimitBehaviorPolicy struct {
	RetryAfterFallbackSeconds int64    `yaml:"retry_after_fallback_seconds"`
	FailOpen                  bool     `yaml:"fail_open"`
	BypassRoutePatterns       []string `yaml:"bypass_route_patterns"`
}
