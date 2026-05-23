package svc_test

import (
	"context"
	"testing"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreSvcImpl "controlplane/internal/core/service"
)

type fakeRuntimeProvider struct{}

func (f *fakeRuntimeProvider) GetPrimary(ctx context.Context, familyCode string) (*coreEntity.RuntimeSecret, error) {
	return &coreEntity.RuntimeSecret{VersionID: "ver-1", FamilyCode: familyCode, Secret: "secret-1", Fingerprint: "fp-1", IsPrimary: true, ActivatedAt: func() *time.Time { value := time.Now().UTC(); return &value }(), NotBefore: time.Now().UTC()}, nil
}
func (f *fakeRuntimeProvider) GetCandidates(ctx context.Context, familyCode string) ([]coreEntity.RuntimeSecret, error) {
	return []coreEntity.RuntimeSecret{{VersionID: "ver-1", FamilyCode: familyCode, Secret: "secret-1", Fingerprint: "fp-1", IsPrimary: true}}, nil
}
func (f *fakeRuntimeProvider) Warm(ctx context.Context, familyCode string) error { return nil }
func (f *fakeRuntimeProvider) Invalidate(familyCode string)                      {}

func TestSecuritySecretProviderMapsPrimary(t *testing.T) {
	provider := coreSvcImpl.NewSecuritySecretProvider(&fakeRuntimeProvider{})
	candidate, err := provider.GetPrimary(context.Background(), "access_token")
	if err != nil {
		t.Fatalf("GetPrimary() error = %v", err)
	}
	if candidate.Family != "access_token" || candidate.Value != "secret-1" || !candidate.IsPrimary {
		t.Fatalf("GetPrimary() returned unexpected candidate: %#v", candidate)
	}
}
