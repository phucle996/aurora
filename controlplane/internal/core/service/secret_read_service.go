package coreSvcImpl

import (
	"context"
	"strings"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreerrorx "controlplane/internal/core/taxonomy"
	coreMetric "controlplane/internal/core/metrics"
	"controlplane/internal/security"
	"controlplane/pkg/logger"
)

type SecretReadService struct {
	repo coreRepoInterface.SecretRepository
	now  func() time.Time
}

// CONTRACT (service-level):
// - Đọc runtime secret family từ DB SoT qua repository, sau đó decrypt candidate.
// - Không fallback ngầm sang nguồn dữ liệu khác khi dependency/read/decrypt lỗi.
// - Trả lỗi thẳng để caller quyết định fail-fast/degrade policy.
//
// BOUNDARY:
// - Chỉ xử lý read-model runtime secrets (family + candidates + primary).
// - Không quyết định transport/status-code hoặc app shutdown policy.
// - Không mutate state của secret lifecycle.
//
// NOTES:
// - `now` để đóng dấu LoadedAt theo thời điểm read service trả dữ liệu.
// - active version set phải tuân invariant tối đa 2 version.
// - decrypt failure được map về domain error để tránh leak chi tiết nội bộ.
//
// CALLER:
// - Core cache-aside runtime provider (internal/core/cache/secret_runtime_provider.go)
// - Bất kỳ bootstrap/runtime wiring nào cần RuntimeSecretFamily read path.
func NewSecretReadService(repo coreRepoInterface.SecretRepository) coreSvcInterface.SecretReadService {
	return &SecretReadService{repo: repo, now: time.Now}
}

// GetRuntimeSecretFamily:
// CONTRACT:
// - familyCode phải hợp lệ (non-empty sau trim).
// - Family phải có primary và active set hợp lệ.
// - Trả plaintext secrets trong candidates để caller runtime provider cache lại.
//
// BOUNDARY:
// - Không retry/fallback read errors ở layer này.
// - Không tự degrade sang data cũ khi decrypt/version-set lỗi.
//
// NOTES:
// - Fail-fast tại callsite: caller phải quyết định stop/degrade khi nhận error.
func (s *SecretReadService) GetRuntimeSecretFamily(ctx context.Context, familyCode string) (*coreEntity.RuntimeSecretFamily, error) {
	startedAt := time.Now().UTC()
	familyCode = strings.TrimSpace(familyCode)
	if familyCode == "" {
		err := coreerrorx.ErrFamilyNotFound
		coreMetric.ObserveSecretLifecycle("read_family", strings.TrimSpace(familyCode), "error", startedAt)
		return nil, err
	}

	family, versions, err := s.loadFamilyState(ctx, familyCode)
	if err != nil {
		coreMetric.ObserveSecretLifecycle("read_family", strings.TrimSpace(familyCode), "error", startedAt)
		logger.SysWarnFields("core.secret.read_family", "failed to load secret family state", err, logger.Fields{"family": familyCode})
		return nil, err
	}

	active := activeVersions(versions)
	if len(active) < 1 || len(active) > 2 {
		err := coreerrorx.ErrInvalidVersionSet
		coreMetric.ObserveSecretLifecycle("read_family", strings.TrimSpace(familyCode), "error", startedAt)
		logger.SysWarnFields("core.secret.read_family", "invalid active secret version set", err, logger.Fields{"family": familyCode, "active_versions": len(active)})
		return nil, err
	}

	candidates := make([]coreEntity.RuntimeSecret, 0, len(active))
	var primary *coreEntity.RuntimeSecret
	for _, item := range active {
		plain, err := security.DecryptSecret(item.SecretCiphertext)
		if err != nil {
			err = coreerrorx.ErrDecryptSecret
			coreMetric.ObserveSecretLifecycle("read_family", strings.TrimSpace(familyCode), "error", startedAt)
			logger.SysWarnFields("core.secret.decrypt", "failed to decrypt runtime secret", err, logger.Fields{"family": familyCode, "version_id": item.ID})
			return nil, err
		}
		runtimeValue := coreEntity.RuntimeSecret{
			VersionID:   item.ID,
			FamilyCode:  family.Code,
			Secret:      plain,
			Fingerprint: item.SecretFingerprint,
			IsPrimary:   item.IsPrimary,
			ActivatedAt: item.ActivatedAt,
			NotBefore:   item.NotBefore,
			NotAfter:    item.NotAfter,
		}
		if runtimeValue.IsPrimary {
			copyValue := runtimeValue
			primary = &copyValue
		}
		candidates = append(candidates, runtimeValue)
	}
	if primary == nil {
		err := coreerrorx.ErrMissingPrimaryVersion
		coreMetric.ObserveSecretLifecycle("read_family", strings.TrimSpace(familyCode), "error", startedAt)
		logger.SysWarnFields("core.secret.read_family", "missing primary secret version", err, logger.Fields{"family": familyCode})
		return nil, err
	}

	coreMetric.ObserveSecretLifecycle("read_family", strings.TrimSpace(familyCode), "ok", startedAt)
	return &coreEntity.RuntimeSecretFamily{
		Family:     *family,
		Primary:    *primary,
		Candidates: candidates,
		LoadedAt:   s.now().UTC(),
	}, nil
}

func (s *SecretReadService) loadFamilyState(ctx context.Context, familyCode string) (*coreEntity.SecretFamily, []coreEntity.SecretVersion, error) {
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
	return family, versions, nil
}
