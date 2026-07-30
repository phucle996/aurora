package hierarchySvcImpl

import (
	"context"
	"crypto/sha256"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyMetrics "controlplane/internal/hierarchy/metrics"

	"github.com/google/uuid"
)

type zoneEncryptionKeyService struct {
	repo hierarchyRepoInterface.ZoneEncryptionKeyRepository
}

func NewZoneEncryptionKeyService(repo hierarchyRepoInterface.ZoneEncryptionKeyRepository) hierarchySvcInterface.ZoneEncryptionKeyService {
	return &zoneEncryptionKeyService{repo: repo}
}

func (s *zoneEncryptionKeyService) RegisterZoneEncryptionKey(ctx context.Context, in *hierarchyEntity.RegisterZoneEncryptionKey) (*hierarchyEntity.RegisterZoneEncryptionKey, error) {
	// [COMMENT]: Resource identity and derived cryptographic metadata are owned
	// by the service. A retried internal workflow keeps any preassigned UUID.
	if in.ID == uuid.Nil {
		id, err := uuid.NewV7()
		if err != nil {
			hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
			return nil, err
		}
		in.ID = id
	}
	fingerprint := sha256.Sum256(in.PublicKey)
	in.Fingerprint = fingerprint[:]
	in.Algorithm = hierarchyEntity.ZoneEncryptionKeyAlgorithm
	in.Status = hierarchyEntity.ZoneEncryptionKeyStatusStaged

	startedAt := time.Now()
	out, err := s.repo.RegisterZoneEncryptionKey(ctx, in)
	if err != nil {
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "RegisterZoneEncryptionKey", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "RegisterZoneEncryptionKey", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return out, nil
}

func (s *zoneEncryptionKeyService) ListZoneEncryptionKeys(ctx context.Context, in *hierarchyEntity.ListZoneEncryptionKeys) ([]hierarchyEntity.ListZoneEncryptionKeys, error) {
	startedAt := time.Now()
	out, err := s.repo.ListZoneEncryptionKeys(ctx, in)
	if err != nil {
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ListZoneEncryptionKeys", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ListZoneEncryptionKeys", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return out, nil
}

func (s *zoneEncryptionKeyService) ActivateZoneEncryptionKey(ctx context.Context, in *hierarchyEntity.ActivateZoneEncryptionKey) (*hierarchyEntity.ActivateZoneEncryptionKey, error) {
	startedAt := time.Now()
	out, err := s.repo.ActivateZoneEncryptionKey(ctx, in)
	if err != nil {
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ActivateZoneEncryptionKey", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "ActivateZoneEncryptionKey", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return out, nil
}

func (s *zoneEncryptionKeyService) RetireZoneEncryptionKey(ctx context.Context, in *hierarchyEntity.RetireZoneEncryptionKey) (*hierarchyEntity.RetireZoneEncryptionKey, error) {
	startedAt := time.Now()
	out, err := s.repo.RetireZoneEncryptionKey(ctx, in)
	if err != nil {
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "RetireZoneEncryptionKey", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "RetireZoneEncryptionKey", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	return out, nil
}
