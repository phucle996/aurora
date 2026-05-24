package coreSvcImpl

import (
	"context"

	coreSvcInterface "controlplane/internal/core/domain/service"
	"controlplane/internal/security"
)

// SecuritySecretProvider là adapter từ RuntimeSecretProvider (core domain)
// sang security.SecretProvider (dùng bởi IAM/middleware).
//
// CONTRACT:
// - Chỉ map shape dữ liệu secret, không chứa policy fallback/failover.
// - Validation input/fallback/fail-fast đặt ở callsite bootstrap/module layer.
// - Provider này không là SoT; SoT vẫn là runtime secret provider + DB phía core.
type SecuritySecretProvider struct {
	provider coreSvcInterface.RuntimeSecretProvider
}

// NewSecuritySecretProvider trả về interface security.SecretProvider để caller
// phụ thuộc theo contract, không buộc concrete type.
//
// CONTRACT: hàm này luôn trả non-nil khi được gọi đúng wiring path
// (runtime provider đã được validate tại callsite bootstrap).
func NewSecuritySecretProvider(provider coreSvcInterface.RuntimeSecretProvider) security.SecretProvider {
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
