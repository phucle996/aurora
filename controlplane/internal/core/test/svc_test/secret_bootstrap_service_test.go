package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreerrorx "controlplane/internal/core/errorx"
	coreSvcImpl "controlplane/internal/core/service"
	"controlplane/internal/security"
)

type fakeLock struct{ released bool }

func (l *fakeLock) Release(ctx context.Context) error { l.released = true; return nil }

type fakeBootstrapRepo struct {
	lock              *fakeLock
	lockErr           error
	family            *coreEntity.SecretFamily
	versions          []coreEntity.SecretVersion
	getFamilyCalls    int
	ensuredFamily     *coreEntity.SecretFamily
	createdVersion    *coreEntity.SecretVersion
	ensuredFamilyErr  error
	createVersionErr  error
	replacePrimaryErr error
	replacedPrimary   string
}

func (f *fakeBootstrapRepo) AcquireFamilyBootstrapLock(ctx context.Context, familyCode string) (coreRepoInterface.SecretBootstrapLock, error) {
	if f.lockErr != nil {
		return nil, f.lockErr
	}
	if f.lock == nil {
		f.lock = &fakeLock{}
	}
	return f.lock, nil
}

func (f *fakeBootstrapRepo) AcquireFamilyRotationLock(ctx context.Context, familyCode string) (coreRepoInterface.SecretRotationLock, error) {
	if f.lockErr != nil {
		return nil, f.lockErr
	}
	if f.lock == nil {
		f.lock = &fakeLock{}
	}
	return f.lock, nil
}
func (f *fakeBootstrapRepo) GetFamilyByCode(ctx context.Context, code string) (*coreEntity.SecretFamily, error) {
	f.getFamilyCalls++
	return f.family, nil
}
func (f *fakeBootstrapRepo) EnsureFamily(ctx context.Context, family coreEntity.SecretFamily) (*coreEntity.SecretFamily, error) {
	if f.ensuredFamilyErr != nil {
		return nil, f.ensuredFamilyErr
	}
	copyValue := family
	f.ensuredFamily = &copyValue
	if f.family == nil {
		f.family = &copyValue
	}
	return f.family, nil
}
func (f *fakeBootstrapRepo) ListVersionsByFamilyID(ctx context.Context, familyID string) ([]coreEntity.SecretVersion, error) {
	return append([]coreEntity.SecretVersion(nil), f.versions...), nil
}
func (f *fakeBootstrapRepo) CreateSecretVersion(ctx context.Context, version coreEntity.SecretVersion) error {
	if f.createVersionErr != nil {
		return f.createVersionErr
	}
	copyValue := version
	f.createdVersion = &copyValue
	return nil
}
func (f *fakeBootstrapRepo) ReplacePrimaryVersion(ctx context.Context, familyID, nextVersionID, previousVersionID string, now time.Time) error {
	if f.replacePrimaryErr != nil {
		return f.replacePrimaryErr
	}
	f.replacedPrimary = nextVersionID + ":" + previousVersionID
	return nil
}
func (f *fakeBootstrapRepo) RetireVersion(ctx context.Context, versionID string, retiredAt time.Time) error {
	return nil
}
func (f *fakeBootstrapRepo) DeleteVersion(ctx context.Context, versionID string) error { return nil }

func TestEnsureInitialSecretVersionCreatesWhenFamilyHasNoUsableVersion(t *testing.T) {
	repo := &fakeBootstrapRepo{family: &coreEntity.SecretFamily{ID: "family-1", Code: "access_token", Name: "Access Token"}}
	security.SetRuntimeMasterKey([]byte("12345678901234567890123456789012"))
	defer security.SetRuntimeMasterKey(nil)
	serviceValue := coreSvcImpl.NewSecretRotationService(repo)

	result, err := serviceValue.EnsureInitialSecretVersion(context.Background(), coreEntity.BootstrapSecretFamily{Code: "access_token", Name: "Access Token"})
	if err != nil {
		t.Fatalf("EnsureInitialSecretVersion() error = %v", err)
	}
	if result == nil || !result.Created {
		t.Fatalf("EnsureInitialSecretVersion() result = %#v, want created", result)
	}
	if repo.createdVersion == nil || repo.createdVersion.FamilyID != "family-1" || !repo.createdVersion.IsPrimary {
		t.Fatalf("CreateSecretVersion() stored unexpected value: %#v", repo.createdVersion)
	}
	if repo.replacedPrimary == "" {
		t.Fatal("ReplacePrimaryVersion() was not called")
	}
	if repo.lock == nil || !repo.lock.released {
		t.Fatal("lock was not released")
	}
	if result.PlainSecret == "" {
		t.Fatal("expected plain secret to be returned on create")
	}
}

func TestEnsureInitialSecretVersionNoopsWhenUsableVersionExists(t *testing.T) {
	repo := &fakeBootstrapRepo{family: &coreEntity.SecretFamily{ID: "family-1", Code: "access_token", Name: "Access Token"}, versions: []coreEntity.SecretVersion{{ID: "ver-1", FamilyID: "family-1", Version: 1, Status: coreEntity.SecretStatusActive, IsPrimary: true}}}
	serviceValue := coreSvcImpl.NewSecretRotationService(repo)
	result, err := serviceValue.EnsureInitialSecretVersion(context.Background(), coreEntity.BootstrapSecretFamily{Code: "access_token", Name: "Access Token"})
	if err != nil {
		t.Fatalf("EnsureInitialSecretVersion() error = %v", err)
	}
	if result == nil || result.Created {
		t.Fatalf("EnsureInitialSecretVersion() result = %#v, want existing/noop", result)
	}
	if repo.createdVersion != nil {
		t.Fatal("CreateSecretVersion() should not be called")
	}
}

func TestEnsureInitialSecretVersionRejectsMoreThanTwoVersions(t *testing.T) {
	repo := &fakeBootstrapRepo{family: &coreEntity.SecretFamily{ID: "family-1", Code: "access_token", Name: "Access Token"}, versions: []coreEntity.SecretVersion{{ID: "ver-1"}, {ID: "ver-2"}, {ID: "ver-3"}}}
	serviceValue := coreSvcImpl.NewSecretRotationService(repo)
	_, err := serviceValue.EnsureInitialSecretVersion(context.Background(), coreEntity.BootstrapSecretFamily{Code: "access_token", Name: "Access Token"})
	if !errors.Is(err, coreerrorx.ErrInvalidVersionSet) {
		t.Fatalf("EnsureInitialSecretVersion() error = %v, want %v", err, coreerrorx.ErrInvalidVersionSet)
	}
}

func (f *fakeBootstrapRepo) RotateFamilyVersions(ctx context.Context, familyID string, nextVersion coreEntity.SecretVersion, previousPrimaryID string, oldestVersionID string, retirePreviousNow bool, now time.Time) error {
	if f.createVersionErr != nil {
		return f.createVersionErr
	}
	if f.replacePrimaryErr != nil {
		return f.replacePrimaryErr
	}
	copyValue := nextVersion
	f.createdVersion = &copyValue
	f.replacedPrimary = nextVersion.ID + ":" + previousPrimaryID
	return nil
}
