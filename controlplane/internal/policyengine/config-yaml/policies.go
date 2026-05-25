package policyengine

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
	AdminCIDR AdminCIDRPolicy `yaml:"admin_cidr"`
}

// AdminCIDRPolicy maps YAML keys for admin CIDR runtime policy.
type AdminCIDRPolicy struct {
	Enabled   bool     `yaml:"enabled"`
	Mode      string   `yaml:"mode"`
	Allowlist []string `yaml:"allowlist"`
}
