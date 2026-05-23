package coreSvcImpl

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sort"
	"strings"
	"time"

	coreCache "controlplane/internal/core/cache"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreerrorx "controlplane/internal/core/errorx"
	coreMetric "controlplane/internal/core/metrics"
	"controlplane/internal/security"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
)

type SecretRotationService struct {
	repo     coreRepoInterface.SecretRepository
	notifier coreCache.SecretInvalidationNotifier
	now      func() time.Time
}

// NewSecretRotationService creates secret rotation service without cache invalidation notifier.
func NewSecretRotationService(repo coreRepoInterface.SecretRepository) *SecretRotationService {
	return &SecretRotationService{
		repo: repo,
		now:  time.Now}
}

// NewSecretRotationServiceWithNotifier creates secret rotation service with notifier
// so runtime caches can be invalidated after bootstrap/rotation success.
func NewSecretRotationServiceWithNotifier(
	repo coreRepoInterface.SecretRepository,
	notifier coreCache.SecretInvalidationNotifier) *SecretRotationService {
	return &SecretRotationService{
		repo:     repo,
		notifier: notifier,
		now:      time.Now,
	}
}

// PlanRotation computes a rotation plan for a family based on current version state and TTL.
// This method is read-only and does not mutate any secret version state.
func (s *SecretRotationService) PlanRotation(ctx context.Context, familyCode string, ttl time.Duration) (*coreEntity.RotationPlan, error) {
	startedAt := time.Now().UTC()
	if ttl <= 0 {
		err := coreerrorx.ErrInvalidTTL
		coreMetric.ObserveSecretLifecycle("plan_rotation", strings.TrimSpace(familyCode), "error", startedAt)
		return nil, err
	}
	family, versions, err := s.loadFamilyState(ctx, familyCode)
	if err != nil {
		coreMetric.ObserveSecretLifecycle("plan_rotation", strings.TrimSpace(familyCode), "error", startedAt)
		return nil, err
	}
	interval := ComputeRotationInterval(ttl)
	coreMetric.ObserveSecretLifecycle("plan_rotation", strings.TrimSpace(familyCode), "ok", startedAt)
	return &coreEntity.RotationPlan{
		Family:           *family,
		CurrentVersions:  versions,
		RotationTTL:      ttl,
		RotationInterval: interval,
		RotateAt:         primaryReferenceTime(versions).Add(interval),
	}, nil
}

// EnsureInitialSecretVersion guarantees a family has at least one usable secret version.
// If a usable version already exists, it returns noop (Created=false).
// If none exists, it creates v1 as active + primary and promotes it.
func (s *SecretRotationService) EnsureInitialSecretVersion(ctx context.Context, family coreEntity.BootstrapSecretFamily) (*coreEntity.EnsureInitialSecretResult, error) {
	startedAt := time.Now().UTC()
	logger.SysInfoFields("core.secret.bootstrap", "ensuring initial secret version", logger.Fields{"family": strings.TrimSpace(family.Code)})
	if strings.TrimSpace(family.Code) == "" || strings.TrimSpace(family.Name) == "" {
		err := coreerrorx.ErrSecretBootstrapFamily
		coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "error", startedAt)
		return nil, err
	}
	lock, err := s.repo.AcquireFamilyBootstrapLock(ctx, family.Code)
	if err != nil {
		coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "error", startedAt)
		logger.SysWarnFields("core.secret.bootstrap", "failed to acquire bootstrap lock", err, logger.Fields{"family": family.Code})
		return nil, err
	}
	defer lock.Release(context.Background())

	dbFamily, err := s.repo.GetFamilyByCode(ctx, family.Code)
	if err != nil {
		coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "error", startedAt)
		return nil, err
	}
	if dbFamily == nil {
		// Family registry row is created once and reused on future bootstrap calls.
		now := s.now().UTC()
		createdFamily, err := s.repo.EnsureFamily(ctx, coreEntity.SecretFamily{
			ID:          newUUIDv7String(),
			Code:        strings.TrimSpace(family.Code),
			Name:        strings.TrimSpace(family.Name),
			Description: strings.TrimSpace(family.Description),
			CreatedAt:   now,
		})
		if err != nil {
			coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "error", startedAt)
			return nil, err
		}
		dbFamily = createdFamily
	}

	versions, err := s.repo.ListVersionsByFamilyID(ctx, dbFamily.ID)
	if err != nil {
		coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "error", startedAt)
		return nil, err
	}
	if len(versions) > 2 {
		err := coreerrorx.ErrInvalidVersionSet
		coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "error", startedAt)
		return nil, err
	}
	usable := activeVersions(versions)
	if len(usable) > 2 {
		err := coreerrorx.ErrInvalidVersionSet
		coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "error", startedAt)
		return nil, err
	}
	if len(usable) >= 1 {
		// Bootstrap is idempotent: if usable versions already exist, do not create new one.
		primary := usable[0]
		for _, item := range usable {
			if item.IsPrimary {
				primary = item
				break
			}
		}
		coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "noop", startedAt)
		logger.SysInfoFields("core.secret.bootstrap", "secret family already has usable version", logger.Fields{"family": family.Code, "version_id": primary.ID})
		return &coreEntity.EnsureInitialSecretResult{Family: *dbFamily, Version: primary, Created: false}, nil
	}

	now := s.now().UTC()
	// Generate plain secret -> encrypt for storage -> derive deterministic fingerprint.
	plain, cipherText, fingerprint, err := generateBootstrapSecretMaterial()
	if err != nil {
		coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "error", startedAt)
		return nil, err
	}
	version := coreEntity.SecretVersion{
		ID:                newUUIDv7String(),
		FamilyID:          dbFamily.ID,
		Version:           1,
		SecretCiphertext:  cipherText,
		SecretFingerprint: fingerprint,
		Status:            coreEntity.SecretStatusActive,
		IsPrimary:         true,
		NotBefore:         now,
		ActivatedAt:       timePointer(now),
		RotationReason:    "bootstrap_initial_secret",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.repo.CreateSecretVersion(ctx, version); err != nil {
		coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "error", startedAt)
		logger.SysWarnFields("core.secret.bootstrap", "failed to create initial secret version", err, logger.Fields{"family": family.Code, "version_id": version.ID})
		return nil, err
	}
	if err := s.repo.ReplacePrimaryVersion(ctx, dbFamily.ID, version.ID, "", now); err != nil {
		coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "error", startedAt)
		logger.SysWarnFields("core.secret.bootstrap", "failed to promote initial secret version", err, logger.Fields{"family": family.Code, "version_id": version.ID})
		return nil, err
	}
	if s.notifier != nil {
		// Best-effort cache invalidation after state-changing operation.
		_ = s.notifier.InvalidateFamily(ctx, dbFamily.Code, "bootstrap")
	}
	coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "created", startedAt)
	logger.SysInfoFields("core.secret.bootstrap", "created initial secret version", logger.Fields{"family": family.Code, "version_id": version.ID})
	return &coreEntity.EnsureInitialSecretResult{Family: *dbFamily, Version: version, Created: true, PlainSecret: plain}, nil
}

// RotateSecretFamily rotates one family to a newly issued version.
// Business invariant: keep only up to two active/pending versions in overlap window.
// New version becomes primary; previous/oldest versions are retired based on input policy.
func (s *SecretRotationService) RotateSecretFamily(ctx context.Context, input coreEntity.RotateSecretFamilyInput) (*coreEntity.SecretVersion, error) {
	startedAt := time.Now().UTC()
	logger.SysInfoFields("core.secret.rotate", "rotating secret family", logger.Fields{"family": strings.TrimSpace(input.FamilyCode)})
	if input.TTL <= 0 {
		err := coreerrorx.ErrInvalidTTL
		coreMetric.ObserveSecretLifecycle("rotate", strings.TrimSpace(input.FamilyCode), "error", startedAt)
		return nil, err
	}
	lock, err := s.repo.AcquireFamilyRotationLock(ctx, input.FamilyCode)
	if err != nil {
		coreMetric.ObserveSecretLifecycle("rotate", strings.TrimSpace(input.FamilyCode), "error", startedAt)
		logger.SysWarnFields("core.secret.rotate", "failed to acquire rotation lock", err, logger.Fields{"family": input.FamilyCode})
		return nil, err
	}
	defer lock.Release(context.Background())

	family, versions, err := s.loadFamilyState(ctx, input.FamilyCode)
	if err != nil {
		coreMetric.ObserveSecretLifecycle("rotate", strings.TrimSpace(input.FamilyCode), "error", startedAt)
		return nil, err
	}
	if input.NewVersion == nil {
		err := coreerrorx.ErrNewVersionRequired
		coreMetric.ObserveSecretLifecycle("rotate", strings.TrimSpace(input.FamilyCode), "error", startedAt)
		return nil, err
	}

	now := s.now().UTC()
	currentActiveVersions := activeVersions(versions)
	if len(currentActiveVersions) < 1 || len(currentActiveVersions) > 2 {
		err := coreerrorx.ErrInvalidVersionSet
		coreMetric.ObserveSecretLifecycle("rotate", strings.TrimSpace(input.FamilyCode), "error", startedAt)
		return nil, err
	}

	nextVersion := *input.NewVersion
	// Service owns operational fields; caller only provides payload material.
	nextVersion.FamilyID = family.ID
	nextVersion.Status = coreEntity.SecretStatusActive
	nextVersion.IsPrimary = true
	nextVersion.NotBefore = now
	nextVersion.ActivatedAt = timePointer(now)
	nextVersion.CreatedAt = now
	nextVersion.UpdatedAt = now
	if strings.TrimSpace(nextVersion.ID) == "" || strings.TrimSpace(nextVersion.SecretCiphertext) == "" || strings.TrimSpace(nextVersion.SecretFingerprint) == "" {
		err := coreerrorx.ErrNewVersionRequired
		coreMetric.ObserveSecretLifecycle("rotate", strings.TrimSpace(input.FamilyCode), "error", startedAt)
		return nil, err
	}

	oldestVersionID := ""
	if len(currentActiveVersions) == 2 {
		// When already in overlap window (2 versions), drop oldest before appending next.
		oldestVersionID = currentActiveVersions[len(currentActiveVersions)-1].ID
		versions = filterVersionByID(versions, oldestVersionID)
		currentActiveVersions = activeVersions(versions)
	}

	previousPrimaryID := ""
	for _, item := range currentActiveVersions {
		if item.IsPrimary {
			previousPrimaryID = item.ID
			break
		}
	}

	if err := s.repo.RotateFamilyVersions(ctx, family.ID, nextVersion, previousPrimaryID, oldestVersionID, input.RetirePreviousNow, now); err != nil {
		coreMetric.ObserveSecretLifecycle("rotate", strings.TrimSpace(input.FamilyCode), "error", startedAt)
		logger.SysWarnFields("core.secret.rotate", "failed to rotate secret family", err, logger.Fields{"family": family.Code, "next_version_id": nextVersion.ID})
		return nil, err
	}
	if s.notifier != nil {
		// Best-effort cache invalidation after successful rotation.
		_ = s.notifier.InvalidateFamily(ctx, family.Code, "rotate")
	}
	coreMetric.ObserveSecretLifecycle("rotate", strings.TrimSpace(input.FamilyCode), "ok", startedAt)
	logger.SysInfoFields("core.secret.rotate", "rotated secret family", logger.Fields{
		"family":              family.Code,
		"next_version_id":     nextVersion.ID,
		"previous_primary_id": previousPrimaryID,
		"dropped_oldest_id":   oldestVersionID,
	})
	return &nextVersion, nil
}

// loadFamilyState loads family and version list, then validates bounded set size.
func (s *SecretRotationService) loadFamilyState(ctx context.Context, familyCode string) (*coreEntity.SecretFamily, []coreEntity.SecretVersion, error) {
	family, err := s.repo.GetFamilyByCode(ctx, strings.TrimSpace(familyCode))
	if err != nil {
		return nil, nil, err
	}
	if family == nil {
		return nil, nil, coreerrorx.ErrFamilyNotFound
	}
	versions, err := s.repo.ListVersionsByFamilyID(ctx, family.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(versions) < 1 || len(versions) > 2 {
		return nil, nil, coreerrorx.ErrInvalidVersionSet
	}
	sort.SliceStable(versions, func(i, j int) bool { return versions[i].Version > versions[j].Version })
	return family, versions, nil
}

// activeVersions keeps only active/pending versions and sorts by descending version number.
func activeVersions(versions []coreEntity.SecretVersion) []coreEntity.SecretVersion {
	result := make([]coreEntity.SecretVersion, 0, len(versions))
	for _, item := range versions {
		if item.Status == coreEntity.SecretStatusActive || item.Status == coreEntity.SecretStatusPending {
			result = append(result, item)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Version > result[j].Version })
	return result
}

// primaryReferenceTime chooses the primary version reference time for planning.
// ActivatedAt is preferred; fallback is CreatedAt.
func primaryReferenceTime(versions []coreEntity.SecretVersion) time.Time {
	for _, item := range versions {
		if item.IsPrimary {
			if item.ActivatedAt != nil {
				return item.ActivatedAt.UTC()
			}
			return item.CreatedAt.UTC()
		}
	}
	return versions[0].CreatedAt.UTC()
}

// filterVersionByID returns a copy without the given version ID.
func filterVersionByID(versions []coreEntity.SecretVersion, versionID string) []coreEntity.SecretVersion {
	filtered := make([]coreEntity.SecretVersion, 0, len(versions))
	for _, item := range versions {
		if item.ID == versionID {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

// timePointer returns pointer for immutable timestamp assignment in entity fields.
func timePointer(value time.Time) *time.Time { return &value }

// newUUIDv7String generates UUIDv7 and falls back to UUIDv4 if UUIDv7 fails.
func newUUIDv7String() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

// generateBootstrapSecretMaterial creates random secret material for bootstrap,
// encrypts it for persistence, and computes a deterministic fingerprint.
func generateBootstrapSecretMaterial() (plain string, cipherText string, fingerprint string, err error) {
	raw := make([]byte, 32)
	if _, err = cryptorand.Read(raw); err != nil {
		return "", "", "", err
	}
	plain = base64.RawURLEncoding.EncodeToString(raw)
	cipherText, err = security.EncryptSecret(plain)
	if err != nil {
		return "", "", "", err
	}
	sum := sha256.Sum256([]byte(plain))
	fingerprint = base64.RawURLEncoding.EncodeToString(sum[:])
	return plain, cipherText, fingerprint, nil
}
