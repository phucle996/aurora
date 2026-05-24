package svc_test

import (
	"context"
	"sync"
	"testing"
	"time"

	coreCache "controlplane/internal/core/cache"
	coreEntity "controlplane/internal/core/domain/entity"
)

type fakeReadService struct {
	mu     sync.Mutex
	calls  int
	result *coreEntity.RuntimeSecretFamily
	err    error
}

func (f *fakeReadService) GetRuntimeSecretFamily(ctx context.Context, familyCode string) (*coreEntity.RuntimeSecretFamily, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.result, f.err
}

func TestCacheAsideSecretProviderHitsServiceOnceThenUsesCache(t *testing.T) {
	readSvc := &fakeReadService{result: &coreEntity.RuntimeSecretFamily{Family: coreEntity.SecretFamily{ID: "family-1", Code: "access_token"}, Primary: coreEntity.RuntimeSecret{VersionID: "ver-1", FamilyCode: "access_token", Secret: "secret-1", IsPrimary: true}, Candidates: []coreEntity.RuntimeSecret{{VersionID: "ver-1", FamilyCode: "access_token", Secret: "secret-1", IsPrimary: true}}, LoadedAt: time.Now().UTC()}}
	provider := coreCache.NewCacheAsideSecretProviderWithTTL(readSvc, time.Minute)
	if _, err := provider.GetPrimary(context.Background(), "access_token"); err != nil {
		t.Fatalf("first GetPrimary() error = %v", err)
	}
	if _, err := provider.GetPrimary(context.Background(), "access_token"); err != nil {
		t.Fatalf("second GetPrimary() error = %v", err)
	}
	if readSvc.calls != 1 {
		t.Fatalf("read service calls = %d, want 1", readSvc.calls)
	}
}

func TestCacheAsideSecretProviderInvalidatesThenReloads(t *testing.T) {
	readSvc := &fakeReadService{result: &coreEntity.RuntimeSecretFamily{Family: coreEntity.SecretFamily{ID: "family-1", Code: "refresh_token"}, Primary: coreEntity.RuntimeSecret{VersionID: "ver-1", FamilyCode: "refresh_token", Secret: "secret-1", IsPrimary: true}, Candidates: []coreEntity.RuntimeSecret{{VersionID: "ver-1", FamilyCode: "refresh_token", Secret: "secret-1", IsPrimary: true}}, LoadedAt: time.Now().UTC()}}
	provider := coreCache.NewCacheAsideSecretProviderWithTTL(readSvc, time.Minute)
	if _, err := provider.GetPrimary(context.Background(), "refresh_token"); err != nil {
		t.Fatalf("first GetPrimary() error = %v", err)
	}
	provider.Invalidate("refresh_token")
	if _, err := provider.GetPrimary(context.Background(), "refresh_token"); err != nil {
		t.Fatalf("second GetPrimary() error = %v", err)
	}
	if readSvc.calls != 2 {
		t.Fatalf("read service calls = %d, want 2", readSvc.calls)
	}
}

func TestCacheAsideSecretProviderTTLExpiryReloads(t *testing.T) {
	readSvc := &fakeReadService{result: &coreEntity.RuntimeSecretFamily{Family: coreEntity.SecretFamily{ID: "family-1", Code: "access_token"}, Primary: coreEntity.RuntimeSecret{VersionID: "ver-1", FamilyCode: "access_token", Secret: "secret-1", IsPrimary: true}, Candidates: []coreEntity.RuntimeSecret{{VersionID: "ver-1", FamilyCode: "access_token", Secret: "secret-1", IsPrimary: true}}, LoadedAt: time.Now().UTC()}}
	provider := coreCache.NewCacheAsideSecretProviderWithTTL(readSvc, time.Millisecond)
	if _, err := provider.GetPrimary(context.Background(), "access_token"); err != nil {
		t.Fatalf("first GetPrimary() error = %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := provider.GetPrimary(context.Background(), "access_token"); err != nil {
		t.Fatalf("second GetPrimary() error = %v", err)
	}
	if readSvc.calls != 2 {
		t.Fatalf("read service calls = %d, want 2", readSvc.calls)
	}
}
