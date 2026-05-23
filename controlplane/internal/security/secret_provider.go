package security

import "context"

const (
	SecretFamilyAccess      = "access_token"
	SecretFamilyRefresh     = "refresh_token"
	SecretFamilyAdminAPIKey = "admin_api_key"
	SecretFamilyOneTime     = "one_time_token"
)

// SecretCandidate is the consumer-facing runtime secret shape used by auth, JWT,
// middleware, and other security-level callers.
type SecretCandidate struct {
	VersionID   string
	Family      string
	Value       string
	Fingerprint string
	IsPrimary   bool
}

// SecretProvider is the consumer-facing secret lookup contract.
//
// Contract:
// - It is intended for packages outside `core`, such as middleware and JWT code.
// - It returns `SecretCandidate`, which is the security-layer shape.
// - Implementations may be backed by `core.RuntimeSecretProvider` through an adapter.
type SecretProvider interface {
	GetPrimary(ctx context.Context, family string) (SecretCandidate, error)
	GetCandidates(ctx context.Context, family string) ([]SecretCandidate, error)
	Warm(ctx context.Context, family string) error
	Invalidate(family string)
}
