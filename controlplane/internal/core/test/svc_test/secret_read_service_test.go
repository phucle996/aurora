package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreerrorx "controlplane/internal/core/taxonomy"
	coreSvcImpl "controlplane/internal/core/service"
	"controlplane/internal/security"
)

func TestSecretReadServiceLoadsPrimaryAndCandidates(t *testing.T) {
	security.SetRuntimeMasterKey([]byte("12345678901234567890123456789012"))
	defer security.SetRuntimeMasterKey(nil)
	cipherPrimary, _ := security.EncryptSecret("primary-secret")
	cipherSecondary, _ := security.EncryptSecret("secondary-secret")
	repo := &fakeSecretRepo{
		family: &coreEntity.SecretFamily{ID: "family-1", Code: "access_token", Name: "Access Token"},
		versions: []coreEntity.SecretVersion{
			{ID: "ver-2", FamilyID: "family-1", Version: 2, SecretCiphertext: cipherPrimary, SecretFingerprint: "fp-2", Status: coreEntity.SecretStatusActive, IsPrimary: true, CreatedAt: time.Now().UTC()},
			{ID: "ver-1", FamilyID: "family-1", Version: 1, SecretCiphertext: cipherSecondary, SecretFingerprint: "fp-1", Status: coreEntity.SecretStatusActive, IsPrimary: false, CreatedAt: time.Now().UTC().Add(-time.Hour)},
		},
	}
	readService := coreSvcImpl.NewSecretReadService(repo)
	result, err := readService.GetRuntimeSecretFamily(context.Background(), "access_token")
	if err != nil {
		t.Fatalf("GetRuntimeSecretFamily() error = %v", err)
	}
	if result.Primary.Secret != "primary-secret" {
		t.Fatalf("Primary.Secret = %q, want primary-secret", result.Primary.Secret)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("len(Candidates) = %d, want 2", len(result.Candidates))
	}
}

func TestSecretReadServiceRejectsMissingPrimary(t *testing.T) {
	security.SetRuntimeMasterKey([]byte("12345678901234567890123456789012"))
	defer security.SetRuntimeMasterKey(nil)
	cipherValue, _ := security.EncryptSecret("secret")
	repo := &fakeSecretRepo{
		family:   &coreEntity.SecretFamily{ID: "family-1", Code: "refresh_token", Name: "Refresh Token"},
		versions: []coreEntity.SecretVersion{{ID: "ver-1", FamilyID: "family-1", Version: 1, SecretCiphertext: cipherValue, SecretFingerprint: "fp-1", Status: coreEntity.SecretStatusActive, IsPrimary: false, CreatedAt: time.Now().UTC()}},
	}
	readService := coreSvcImpl.NewSecretReadService(repo)
	_, err := readService.GetRuntimeSecretFamily(context.Background(), "refresh_token")
	if !errors.Is(err, coreerrorx.ErrMissingPrimaryVersion) {
		t.Fatalf("GetRuntimeSecretFamily() error = %v, want %v", err, coreerrorx.ErrMissingPrimaryVersion)
	}
}
