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
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreerrorx "controlplane/internal/core/errorx"
	coreMetric "controlplane/internal/core/metrics"
	"controlplane/internal/security"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
)

const (
	rotationReasonBootstrapInitialSecret = "bootstrap_initial_secret"
	invalidateReasonBootstrap            = "bootstrap"
	invalidateReasonRotate               = "rotate"
	minRotationInterval                  = 24 * time.Hour
	rotationIntervalMultiplier           = 2
	bootstrapSecretBytes                 = 32
	bootstrapInitialVersion              = 1
)

type SecretRotationService struct {
	repo     coreRepoInterface.SecretRepository
	notifier coreCache.SecretInvalidationNotifier
	now      func() time.Time
}

type noopInvalidationNotifier struct{}

func (noopInvalidationNotifier) InvalidateFamily(ctx context.Context, familyCode string, reason string) error {
	return nil
}

// CONTRACT (module-level):
// - DB/repository là SoT cho secret families + versions.
// - Mọi mutate flow phải đi qua lock tương ứng (bootstrap/rotation lock).
// - Invariant version-set: tối đa 2 bản active/pending trong overlap window.
// - Invalidation notifier chỉ là best-effort side effect sau mutate thành công.
//
// BOUNDARY:
// - Service này quản business rule của lifecycle secret (plan/bootstrap/rotate).
// - Service không quyết định shutdown policy hay fallback policy toàn app.
// - Mapping lỗi sang transport/status code là trách nhiệm caller layer trên.
//
// NOTES:
// - `now` được inject để test deterministic.
// - Metrics/logging phục vụ observability, không thay đổi outcome nghiệp vụ.

// NewSecretRotationService trả interface rotation service để caller phụ thuộc
// theo contract domain thay vì concrete implementation.
//
// CONTRACT:
// - Service này không fallback dependency ngầm.
// - Validation input/fail-fast được quyết định tại callsite bootstrap/use-case.
// - Constructor luôn trả non-nil theo wiring path hợp lệ.
func NewSecretRotationService(
	repo coreRepoInterface.SecretRepository,
	notifier coreCache.SecretInvalidationNotifier,
) coreSvcInterface.SecretRotationService {
	// Policy by callsite:
	// - Bootstrap path có thể truyền notifier=nil (single-node init, không bắt buộc fan-out invalidate).
	// - Rotate worker/scheduler path phải truyền notifier!=nil để publish invalidation
	//   cho các node khác sau khi rotate thành công.
	if notifier == nil {
		notifier = noopInvalidationNotifier{}
	}
	return &SecretRotationService{
		repo:     repo,
		notifier: notifier,
		now:      time.Now,
	}
}

// PlanRotation:
// CONTRACT:
// - TTL phải > 0.
// - Family phải tồn tại và version-set hợp lệ.
// - Read-only: không mutate DB/cache state.
//
// BOUNDARY:
// - Chỉ trả kế hoạch rotate để caller quyết định trigger rotate thật hay không.
//
// NOTES:
// - `RotateAt` = primaryReferenceTime + ComputeRotationInterval(ttl).
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

// EnsureInitialSecretVersion:
// CONTRACT:
// - Idempotent bootstrap theo family.
// - Nếu đã có usable version -> noop (Created=false).
// - Nếu chưa có usable version -> tạo v1 active + primary.
// - Luôn chạy dưới bootstrap lock của family.
//
// BOUNDARY:
// - Chỉ xử lý bootstrap lifecycle cho 1 family, không mở rộng policy app-level.
//
// NOTES:
// - `PlainSecret` chỉ trả khi created=true.
// - Cache invalidation sau mutate là best-effort.
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
		familyID, idErr := uuid.NewV7()
		if idErr != nil {
			familyID = uuid.New()
		}
		createdFamily, err := s.repo.EnsureFamily(ctx, coreEntity.SecretFamily{
			ID:          familyID.String(),
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
	versionID, versionIDErr := uuid.NewV7()
	if versionIDErr != nil {
		versionID = uuid.New()
	}
	version := coreEntity.SecretVersion{
		ID:                versionID.String(),
		FamilyID:          dbFamily.ID,
		Version:           bootstrapInitialVersion,
		SecretCiphertext:  cipherText,
		SecretFingerprint: fingerprint,
		Status:            coreEntity.SecretStatusActive,
		IsPrimary:         true,
		NotBefore:         now,
		ActivatedAt:       &now,
		RotationReason:    rotationReasonBootstrapInitialSecret,
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
	// Bootstrap mutate thành công -> invalidate best-effort.
	// Nếu notifier là noop (bootstrap local) thì call này no-op theo policy constructor.
	if notifyErr := s.notifier.InvalidateFamily(ctx, dbFamily.Code, invalidateReasonBootstrap); notifyErr != nil {
		logger.SysWarnFields("core.secret.bootstrap", "failed to invalidate runtime cache after bootstrap", notifyErr, logger.Fields{"family": dbFamily.Code, "reason": invalidateReasonBootstrap})
	}
	coreMetric.ObserveSecretLifecycle("bootstrap", strings.TrimSpace(family.Code), "created", startedAt)
	logger.SysInfoFields("core.secret.bootstrap", "created initial secret version", logger.Fields{"family": family.Code, "version_id": version.ID})
	return &coreEntity.EnsureInitialSecretResult{Family: *dbFamily, Version: version, Created: true, PlainSecret: plain}, nil
}

// RotateSecretFamily:
// CONTRACT:
// - TTL phải hợp lệ, NewVersion payload phải đầy đủ.
// - Rotate chạy dưới family rotation lock.
// - Duy trì invariant tối đa 2 active/pending versions.
// - New version trở thành primary; previous/oldest retire theo input policy.
//
// BOUNDARY:
// - Không retry lock conflict ở layer này.
// - Không fallback âm thầm khi dependency mutate lỗi.
//
// NOTES:
// - Nếu đang có 2 version, drop oldest trước khi append version mới.
// - Invalidation notifier gọi best-effort sau rotate thành công.
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
	nextVersion.ActivatedAt = &now
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
		filtered := make([]coreEntity.SecretVersion, 0, len(versions))
		for _, item := range versions {
			if item.ID == oldestVersionID {
				continue
			}
			filtered = append(filtered, item)
		}
		versions = filtered
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
	// Rotate mutate thành công -> invalidate best-effort.
	// Rotate worker phải cấp notifier bus thật để fan-out invalidate đa node.
	if notifyErr := s.notifier.InvalidateFamily(ctx, family.Code, invalidateReasonRotate); notifyErr != nil {
		logger.SysWarnFields("core.secret.rotate", "failed to invalidate runtime cache after rotate", notifyErr, logger.Fields{"family": family.Code, "reason": invalidateReasonRotate})
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

// ComputeRotationInterval derives rotation interval from TTL policy.
// Policy rationale:
// - giữ floor 24h để tránh tần suất rotate quá dày khi TTL nhỏ bất thường.
// - khi TTL đủ lớn, interval = TTL * 2 để duy trì overlap window tối đa 2 version.
func ComputeRotationInterval(ttl time.Duration) time.Duration {
	if ttl < minRotationInterval {
		return minRotationInterval
	}
	return ttl * rotationIntervalMultiplier
}

// generateBootstrapSecretMaterial creates random secret material for bootstrap,
// encrypts it for persistence, and computes a deterministic fingerprint.
func generateBootstrapSecretMaterial() (plain string, cipherText string, fingerprint string, err error) {
	raw := make([]byte, bootstrapSecretBytes)
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
