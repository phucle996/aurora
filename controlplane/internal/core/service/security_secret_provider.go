package coreSvcImpl

import (
	"context"

	coreSvcInterface "controlplane/internal/core/domain/service"
	"controlplane/internal/security"
)

type SecuritySecretProvider struct {
	provider coreSvcInterface.RuntimeSecretProvider
}

func NewSecuritySecretProvider(provider coreSvcInterface.RuntimeSecretProvider) *SecuritySecretProvider {
	return &SecuritySecretProvider{provider: provider}
}

func (p *SecuritySecretProvider) GetPrimary(ctx context.Context, family string) (security.SecretCandidate, error) {
	runtimeSecret, err := p.provider.GetPrimary(ctx, family)
	if err != nil {
		return security.SecretCandidate{}, err
	}
	return security.SecretCandidate{
		VersionID:   runtimeSecret.VersionID,
		Family:      runtimeSecret.FamilyCode,
		Value:       runtimeSecret.Secret,
		Fingerprint: runtimeSecret.Fingerprint,
		IsPrimary:   runtimeSecret.IsPrimary,
	}, nil
}

func (p *SecuritySecretProvider) GetCandidates(ctx context.Context, family string) ([]security.SecretCandidate, error) {
	runtimeSecrets, err := p.provider.GetCandidates(ctx, family)
	if err != nil {
		return nil, err
	}
	result := make([]security.SecretCandidate, 0, len(runtimeSecrets))
	for _, item := range runtimeSecrets {
		result = append(result, security.SecretCandidate{
			VersionID:   item.VersionID,
			Family:      item.FamilyCode,
			Value:       item.Secret,
			Fingerprint: item.Fingerprint,
			IsPrimary:   item.IsPrimary,
		})
	}
	return result, nil
}

func (p *SecuritySecretProvider) Warm(ctx context.Context, family string) error {
	return p.provider.Warm(ctx, family)
}

func (p *SecuritySecretProvider) Invalidate(family string) {
	p.provider.Invalidate(family)
}

var _ security.SecretProvider = (*SecuritySecretProvider)(nil)
