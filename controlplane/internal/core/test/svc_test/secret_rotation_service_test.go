package svc_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcImpl "controlplane/internal/core/service"
	"controlplane/internal/security"
	"github.com/redis/go-redis/v9"
)

type fakeLock struct {
	released bool
}

func (l *fakeLock) Release(ctx context.Context) error {
	l.released = true
	return nil
}

type fakeSecretRepo struct {
	bootstrapLock    *fakeLock
	rotationLock     *fakeLock
	bootstrapLockErr error
	rotationLockErr  error
	secrets          map[string]*coreEntity.CoreSecretRow
	saveErr          error
	updateErr        error
}

func (f *fakeSecretRepo) AcquireSecretTypeBootstrapLock(ctx context.Context, secretType string) (coreRepoInterface.SecretBootstrapLock, error) {
	if f.bootstrapLockErr != nil {
		return nil, f.bootstrapLockErr
	}
	f.bootstrapLock = &fakeLock{}
	return f.bootstrapLock, nil
}

func (f *fakeSecretRepo) AcquireSecretTypeRotationLock(ctx context.Context, secretType string) (coreRepoInterface.SecretRotationLock, error) {
	if f.rotationLockErr != nil {
		return nil, f.rotationLockErr
	}
	f.rotationLock = &fakeLock{}
	return f.rotationLock, nil
}

func (f *fakeSecretRepo) GetSecretsByType(ctx context.Context, secretType string) (*coreEntity.RuntimeSecrets, error) {
	row, ok := f.secrets[secretType]
	if !ok || row == nil {
		return nil, nil
	}
	activePlain, err := security.DecryptSecretBytes(row.ActiveSecret)
	if err != nil {
		return nil, err
	}
	standbyPlain, err := security.DecryptSecretBytes(row.StandbySecret)
	if err != nil {
		return nil, err
	}
	return &coreEntity.RuntimeSecrets{
		SecretType: row.SecretType,
		Active: coreEntity.RuntimeSecret{
			Secret:      activePlain,
			Fingerprint: row.ActiveFingerprint,
			CreatedAt:   row.ActiveCreatedAt,
		},
		Standby: coreEntity.RuntimeSecret{
			Secret:      standbyPlain,
			Fingerprint: row.StandbyFingerprint,
			CreatedAt:   row.StandbyCreatedAt,
		},
		LoadedAt: time.Now().UTC(),
	}, nil
}

func (f *fakeSecretRepo) SaveSecrets(ctx context.Context, row coreEntity.CoreSecretRow) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.secrets[row.SecretType] = &row
	return nil
}

func (f *fakeSecretRepo) UpdateSecrets(ctx context.Context, secretType string, activeSecret, activeFingerprint string, standbySecret, standbyFingerprint string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	row, ok := f.secrets[secretType]
	if !ok {
		return errors.New("not found")
	}
	row.ActiveSecret = activeSecret
	row.ActiveFingerprint = activeFingerprint
	row.StandbySecret = standbySecret
	row.StandbyFingerprint = standbyFingerprint
	row.UpdatedAt = time.Now().UTC()
	return nil
}

func (f *fakeSecretRepo) GetAccessSecret(ctx context.Context) (*coreEntity.RuntimeSecrets, error) {
	return f.GetSecretsByType(ctx, "access_secret")
}

func (f *fakeSecretRepo) GetRefreshSecret(ctx context.Context) (*coreEntity.RuntimeSecrets, error) {
	return f.GetSecretsByType(ctx, "refresh_secret")
}

func (f *fakeSecretRepo) GetAdminAPIKey(ctx context.Context) (*coreEntity.RuntimeSecrets, error) {
	return f.GetSecretsByType(ctx, "admin_api_key")
}

func (f *fakeSecretRepo) GetOneTimeTokenSecret(ctx context.Context) (*coreEntity.RuntimeSecrets, error) {
	return f.GetSecretsByType(ctx, "one_time_token")
}

func makeTestRegistryAndFanout() (*cacheengine.CacheRegistry, *cacheengine.RedisFanout) {
	registry := cacheengine.NewCacheRegistry(cacheengine.NewShardedCache())
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:9999"})
	fanout := cacheengine.NewRedisFanout(rdb, "test-channel", registry)
	return registry, fanout
}

func TestEnsureInitialSecretsCreatesNewSecrets(t *testing.T) {
	security.SetRuntimeMasterKey([]byte("12345678901234567890123456789012"))
	defer security.SetRuntimeMasterKey(nil)

	repo := &fakeSecretRepo{secrets: make(map[string]*coreEntity.CoreSecretRow)}
	registry, fanout := makeTestRegistryAndFanout()
	svc := coreSvcImpl.NewSecretRotationService(repo, registry, fanout)

	res, err := svc.EnsureInitialSecrets(context.Background(), "access_secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res == nil {
		t.Fatal("expected non-nil response")
	}

	if len(res.Active.Secret) == 0 || len(res.Standby.Secret) == 0 {
		t.Fatal("expected active and standby secrets to be initialized")
	}

	if repo.bootstrapLock == nil || !repo.bootstrapLock.released {
		t.Fatal("expected bootstrap lock to be acquired and released")
	}

	// Idempotency check
	res2, err := svc.EnsureInitialSecrets(context.Background(), "access_secret")
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}

	if !bytes.Equal(res2.Active.Secret, res.Active.Secret) || !bytes.Equal(res2.Standby.Secret, res.Standby.Secret) {
		t.Fatal("expected EnsureInitialSecrets to be idempotent")
	}
}

func TestRotateSecretPromotesActiveToStandbyAndGeneratesNewActive(t *testing.T) {
	security.SetRuntimeMasterKey([]byte("12345678901234567890123456789012"))
	defer security.SetRuntimeMasterKey(nil)

	repo := &fakeSecretRepo{secrets: make(map[string]*coreEntity.CoreSecretRow)}
	registry, fanout := makeTestRegistryAndFanout()
	svc := coreSvcImpl.NewSecretRotationService(repo, registry, fanout)

	// Override clock to allow immediate rotation after bootstrap
	svcImpl := svc.(*coreSvcImpl.SecretRotationService)
	nowTime := time.Now().UTC()
	svcImpl.Now = func() time.Time { return nowTime }

	// Bootstrap first
	initial, err := svc.EnsureInitialSecrets(context.Background(), "access_secret")
	if err != nil {
		t.Fatalf("failed to bootstrap: %v", err)
	}

	oldActive := initial.Active.Secret

	// Advance clock by 1 hour to bypass double-rotation prevention
	nowTime = nowTime.Add(1 * time.Hour)

	// Rotate
	rotated, err := svc.RotateSecret(context.Background(), "access_secret")
	if err != nil {
		t.Fatalf("unexpected error on rotation: %v", err)
	}

	if !bytes.Equal(rotated.Standby.Secret, oldActive) {
		t.Fatalf("expected old active secret (%s) to become standby secret (%s)", oldActive, rotated.Standby.Secret)
	}

	if bytes.Equal(rotated.Active.Secret, oldActive) {
		t.Fatal("expected new active secret to be generated")
	}

	if repo.rotationLock == nil || !repo.rotationLock.released {
		t.Fatal("expected rotation lock to be acquired and released")
	}
}

func TestRotateSecretCooldown(t *testing.T) {
	security.SetRuntimeMasterKey([]byte("12345678901234567890123456789012"))
	defer security.SetRuntimeMasterKey(nil)

	repo := &fakeSecretRepo{secrets: make(map[string]*coreEntity.CoreSecretRow)}
	
	registry, fanout := makeTestRegistryAndFanout()
	svc := coreSvcImpl.NewSecretRotationService(repo, registry, fanout)
	
	svcImpl := svc.(*coreSvcImpl.SecretRotationService)
	nowTime := time.Now().UTC()
	svcImpl.Now = func() time.Time { return nowTime }
	
	// Bootstrap
	_, err := svc.EnsureInitialSecrets(context.Background(), "access_secret")
	if err != nil {
		t.Fatalf("failed to bootstrap: %v", err)
	}

	// Advance clock by 1 hour to bypass double-rotation prevention
	nowTime = nowTime.Add(1 * time.Hour)

	repo.rotationLock = nil

	// First rotation
	r1, err := svc.RotateSecret(context.Background(), "access_secret")
	if err != nil {
		t.Fatalf("first rotate failed: %v", err)
	}
	if repo.rotationLock == nil {
		t.Fatal("expected rotation lock to be acquired on first rotation")
	}

	// Reset lock tracker
	repo.rotationLock = nil

	// Second rotation immediately (cooldown active)
	r2, err := svc.RotateSecret(context.Background(), "access_secret")
	if err != nil {
		t.Fatalf("second rotate failed: %v", err)
	}

	if repo.rotationLock != nil {
		t.Fatal("expected rotation lock NOT to be acquired due to cooldown")
	}

	if !bytes.Equal(r1.Active.Secret, r2.Active.Secret) {
		t.Fatal("expected returned secrets to be unchanged during cooldown")
	}
}
