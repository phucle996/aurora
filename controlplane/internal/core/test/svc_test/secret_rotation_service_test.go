package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreerrorx "controlplane/internal/core/taxonomy"
	coreSvcImpl "controlplane/internal/core/service"
)

type fakeNoopLock struct{ released bool }

func (l *fakeNoopLock) Release(ctx context.Context) error { l.released = true; return nil }

type fakeSecretRepo struct {
	rotationLock      *fakeNoopLock
	rotationLockErr   error
	family            *coreEntity.SecretFamily
	versions          []coreEntity.SecretVersion
	created           *coreEntity.SecretVersion
	replacedPrimaryID string
	deletedVersionID  string
	retiredVersionID  string
	familyErr         error
	versionsErr       error
	createErr         error
	replaceErr        error
	retireErr         error
	deleteErr         error
}

func (f *fakeSecretRepo) AcquireFamilyBootstrapLock(ctx context.Context, familyCode string) (coreRepoInterface.SecretBootstrapLock, error) {
	return &fakeNoopLock{}, nil
}

func (f *fakeSecretRepo) AcquireFamilyRotationLock(ctx context.Context, familyCode string) (coreRepoInterface.SecretRotationLock, error) {
	if f.rotationLockErr != nil {
		return nil, f.rotationLockErr
	}
	if f.rotationLock == nil {
		f.rotationLock = &fakeNoopLock{}
	}
	return f.rotationLock, nil
}

func (f *fakeSecretRepo) EnsureFamily(ctx context.Context, family coreEntity.SecretFamily) (*coreEntity.SecretFamily, error) {
	if f.family == nil {
		copyValue := family
		f.family = &copyValue
	}
	return f.family, nil
}

func (f *fakeSecretRepo) GetFamilyByCode(ctx context.Context, code string) (*coreEntity.SecretFamily, error) {
	return f.family, f.familyErr
}

func (f *fakeSecretRepo) ListVersionsByFamilyID(ctx context.Context, familyID string) ([]coreEntity.SecretVersion, error) {
	return append([]coreEntity.SecretVersion(nil), f.versions...), f.versionsErr
}

func (f *fakeSecretRepo) CreateSecretVersion(ctx context.Context, version coreEntity.SecretVersion) error {
	if f.createErr != nil {
		return f.createErr
	}
	copyValue := version
	f.created = &copyValue
	return nil
}

func (f *fakeSecretRepo) ReplacePrimaryVersion(ctx context.Context, familyID string, nextVersionID string, previousVersionID string, now time.Time) error {
	if f.replaceErr != nil {
		return f.replaceErr
	}
	f.replacedPrimaryID = nextVersionID + ":" + previousVersionID
	return nil
}

func (f *fakeSecretRepo) RetireVersion(ctx context.Context, versionID string, retiredAt time.Time) error {
	if f.retireErr != nil {
		return f.retireErr
	}
	f.retiredVersionID = versionID
	return nil
}

func (f *fakeSecretRepo) DeleteVersion(ctx context.Context, versionID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedVersionID = versionID
	return nil
}

func (f *fakeSecretRepo) RotateFamilyVersions(ctx context.Context, familyID string, nextVersion coreEntity.SecretVersion, previousPrimaryID string, oldestVersionID string, retirePreviousNow bool, now time.Time) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if f.createErr != nil {
		return f.createErr
	}
	if f.replaceErr != nil {
		return f.replaceErr
	}
	if retirePreviousNow && f.retireErr != nil {
		return f.retireErr
	}
	if oldestVersionID != "" {
		f.deletedVersionID = oldestVersionID
	}
	copyValue := nextVersion
	f.created = &copyValue
	f.replacedPrimaryID = nextVersion.ID + ":" + previousPrimaryID
	if retirePreviousNow && previousPrimaryID != "" {
		f.retiredVersionID = previousPrimaryID
	}
	return nil
}

func TestRotateSecretFamilyWithOneKeyCreatesSecondKey(t *testing.T) {
	repo := &fakeSecretRepo{
		family: &coreEntity.SecretFamily{ID: "family-1", Code: "access_token"},
		versions: []coreEntity.SecretVersion{
			{ID: "ver-1", FamilyID: "family-1", Version: 1, Status: coreEntity.SecretStatusActive, IsPrimary: true, CreatedAt: time.Now().UTC()},
		},
	}
	serviceValue := coreSvcImpl.NewSecretRotationService(repo, nil)

	newVersion, err := serviceValue.RotateSecretFamily(context.Background(), coreEntity.RotateSecretFamilyInput{
		FamilyCode: "access_token",
		TTL:        2 * time.Hour,
		NewVersion: &coreEntity.SecretVersion{ID: "ver-2", Version: 2, SecretCiphertext: "cipher-2", SecretFingerprint: "fp-2"},
	})
	if err != nil {
		t.Fatalf("RotateSecretFamily() error = %v", err)
	}
	if newVersion == nil || newVersion.ID != "ver-2" {
		t.Fatalf("RotateSecretFamily() returned unexpected version: %#v", newVersion)
	}
	if repo.deletedVersionID != "" {
		t.Fatalf("DeleteVersion() = %q, want empty", repo.deletedVersionID)
	}
	if repo.replacedPrimaryID != "ver-2:ver-1" {
		t.Fatalf("ReplacePrimaryVersion() = %q, want ver-2:ver-1", repo.replacedPrimaryID)
	}
	if repo.created == nil || repo.created.FamilyID != "family-1" || !repo.created.IsPrimary {
		t.Fatalf("CreateSecretVersion() stored unexpected value: %#v", repo.created)
	}
	if repo.rotationLock == nil || !repo.rotationLock.released {
		t.Fatal("rotation lock was not released")
	}
}

func TestRotateSecretFamilyWithTwoKeysDropsOldestThenRotates(t *testing.T) {
	repo := &fakeSecretRepo{
		family: &coreEntity.SecretFamily{ID: "family-1", Code: "access_token"},
		versions: []coreEntity.SecretVersion{
			{ID: "ver-2", FamilyID: "family-1", Version: 2, Status: coreEntity.SecretStatusActive, IsPrimary: true, CreatedAt: time.Now().UTC()},
			{ID: "ver-1", FamilyID: "family-1", Version: 1, Status: coreEntity.SecretStatusActive, IsPrimary: false, CreatedAt: time.Now().UTC().Add(-time.Hour)},
		},
	}
	serviceValue := coreSvcImpl.NewSecretRotationService(repo, nil)

	newVersion, err := serviceValue.RotateSecretFamily(context.Background(), coreEntity.RotateSecretFamilyInput{
		FamilyCode: "access_token",
		TTL:        2 * time.Hour,
		NewVersion: &coreEntity.SecretVersion{ID: "ver-3", Version: 3, SecretCiphertext: "cipher-3", SecretFingerprint: "fp-3"},
	})
	if err != nil {
		t.Fatalf("RotateSecretFamily() error = %v", err)
	}
	if newVersion == nil || newVersion.ID != "ver-3" {
		t.Fatalf("RotateSecretFamily() returned unexpected version: %#v", newVersion)
	}
	if repo.deletedVersionID != "ver-1" {
		t.Fatalf("DeleteVersion() = %q, want ver-1", repo.deletedVersionID)
	}
	if repo.replacedPrimaryID != "ver-3:ver-2" {
		t.Fatalf("ReplacePrimaryVersion() = %q, want ver-3:ver-2", repo.replacedPrimaryID)
	}
}

func TestRotateSecretFamilyRejectsThreeKeys(t *testing.T) {
	repo := &fakeSecretRepo{
		family: &coreEntity.SecretFamily{ID: "family-1", Code: "access_token"},
		versions: []coreEntity.SecretVersion{
			{ID: "ver-3", FamilyID: "family-1", Version: 3, Status: coreEntity.SecretStatusActive, IsPrimary: true, CreatedAt: time.Now().UTC()},
			{ID: "ver-2", FamilyID: "family-1", Version: 2, Status: coreEntity.SecretStatusActive, IsPrimary: false, CreatedAt: time.Now().UTC().Add(-time.Hour)},
			{ID: "ver-1", FamilyID: "family-1", Version: 1, Status: coreEntity.SecretStatusRetired, IsPrimary: false, CreatedAt: time.Now().UTC().Add(-2 * time.Hour)},
		},
	}
	serviceValue := coreSvcImpl.NewSecretRotationService(repo, nil)

	_, err := serviceValue.RotateSecretFamily(context.Background(), coreEntity.RotateSecretFamilyInput{
		FamilyCode: "access_token",
		TTL:        2 * time.Hour,
		NewVersion: &coreEntity.SecretVersion{ID: "ver-4", Version: 4, SecretCiphertext: "cipher-4", SecretFingerprint: "fp-4"},
	})
	if !errors.Is(err, coreerrorx.ErrInvalidVersionSet) {
		t.Fatalf("RotateSecretFamily() error = %v, want %v", err, coreerrorx.ErrInvalidVersionSet)
	}
}

func TestRotateSecretFamilyRejectsFamilyNotFound(t *testing.T) {
	repo := &fakeSecretRepo{}
	serviceValue := coreSvcImpl.NewSecretRotationService(repo, nil)

	_, err := serviceValue.RotateSecretFamily(context.Background(), coreEntity.RotateSecretFamilyInput{
		FamilyCode: "missing",
		TTL:        2 * time.Hour,
		NewVersion: &coreEntity.SecretVersion{ID: "ver-1", Version: 1, SecretCiphertext: "cipher", SecretFingerprint: "fp"},
	})
	if !errors.Is(err, coreerrorx.ErrFamilyNotFound) {
		t.Fatalf("RotateSecretFamily() error = %v, want %v", err, coreerrorx.ErrFamilyNotFound)
	}
}

func TestRotateSecretFamilyRejectsInvalidTTL(t *testing.T) {
	repo := &fakeSecretRepo{
		family: &coreEntity.SecretFamily{ID: "family-1", Code: "access_token"},
		versions: []coreEntity.SecretVersion{
			{ID: "ver-1", FamilyID: "family-1", Version: 1, Status: coreEntity.SecretStatusActive, IsPrimary: true, CreatedAt: time.Now().UTC()},
		},
	}
	serviceValue := coreSvcImpl.NewSecretRotationService(repo, nil)

	_, err := serviceValue.RotateSecretFamily(context.Background(), coreEntity.RotateSecretFamilyInput{
		FamilyCode: "access_token",
		TTL:        0,
		NewVersion: &coreEntity.SecretVersion{ID: "ver-2", Version: 2, SecretCiphertext: "cipher-2", SecretFingerprint: "fp-2"},
	})
	if !errors.Is(err, coreerrorx.ErrInvalidTTL) {
		t.Fatalf("RotateSecretFamily() error = %v, want %v", err, coreerrorx.ErrInvalidTTL)
	}
}

func TestPlanRotationRejectsZeroVersions(t *testing.T) {
	repo := &fakeSecretRepo{family: &coreEntity.SecretFamily{ID: "family-1", Code: "refresh_token"}}
	serviceValue := coreSvcImpl.NewSecretRotationService(repo, nil)
	_, err := serviceValue.PlanRotation(context.Background(), "refresh_token", 2*time.Hour)
	if err == nil {
		t.Fatal("PlanRotation() error = nil, want error")
	}
}

func TestRotateSecretFamilyReturnsLockError(t *testing.T) {
	repo := &fakeSecretRepo{rotationLockErr: errors.New("lock failed")}
	serviceValue := coreSvcImpl.NewSecretRotationService(repo, nil)

	_, err := serviceValue.RotateSecretFamily(context.Background(), coreEntity.RotateSecretFamilyInput{
		FamilyCode: "access_token",
		TTL:        2 * time.Hour,
		NewVersion: &coreEntity.SecretVersion{ID: "ver-2", Version: 2, SecretCiphertext: "cipher-2", SecretFingerprint: "fp-2"},
	})
	if err == nil || err.Error() != "lock failed" {
		t.Fatalf("RotateSecretFamily() error = %v, want lock failed", err)
	}
}
