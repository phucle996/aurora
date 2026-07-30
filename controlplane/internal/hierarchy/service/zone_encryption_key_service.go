package service

import (
	"context"
	"crypto/sha256"
	"time"

	entity "controlplane/internal/hierarchy/domain/entity"
	hierarchyrepo "controlplane/internal/hierarchy/domain/repo"
	hierarchyservice "controlplane/internal/hierarchy/domain/service"
	metrics "controlplane/internal/hierarchy/metrics"

	"github.com/google/uuid"
)

type zoneEncryptionKeyService struct {
	repo hierarchyrepo.ZoneEncryptionKeyRepository
}

func NewZoneEncryptionKeyService(repo hierarchyrepo.ZoneEncryptionKeyRepository) hierarchyservice.ZoneEncryptionKeyService {
	return &zoneEncryptionKeyService{repo: repo}
}

func (s *zoneEncryptionKeyService) RegisterZoneEncryptionKey(ctx context.Context, in *entity.RegisterZoneEncryptionKey) (*entity.RegisterZoneEncryptionKey, error) {
	// [COMMENT]: Resource identity and derived cryptographic metadata are owned
	// by the service. A retried internal workflow keeps any preassigned UUID.
	if in.ID == uuid.Nil {
		id, err := uuid.NewV7()
		if err != nil {
			metrics.ServiceCall(ctx, metrics.OutcomeFailure)
			return nil, err
		}
		in.ID = id
	}
	fingerprint := sha256.Sum256(in.PublicKey)
	in.Fingerprint = fingerprint[:]
	in.Algorithm = entity.ZoneEncryptionKeyAlgorithm
	in.Status = entity.ZoneEncryptionKeyStatusStaged

	startedAt := time.Now()
	out, err := s.repo.RegisterZoneEncryptionKey(ctx, in)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "RegisterZoneEncryptionKey", metrics.OutcomeFailure, time.Since(startedAt), err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}
	metrics.Downstream(ctx, metrics.KindRepo, "RegisterZoneEncryptionKey", metrics.OutcomeSuccess, time.Since(startedAt), nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return out, nil
}

func (s *zoneEncryptionKeyService) ListZoneEncryptionKeys(ctx context.Context, in *entity.ListZoneEncryptionKeys) ([]entity.ListZoneEncryptionKeys, error) {
	startedAt := time.Now()
	out, err := s.repo.ListZoneEncryptionKeys(ctx, in)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "ListZoneEncryptionKeys", metrics.OutcomeFailure, time.Since(startedAt), err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}
	metrics.Downstream(ctx, metrics.KindRepo, "ListZoneEncryptionKeys", metrics.OutcomeSuccess, time.Since(startedAt), nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return out, nil
}

func (s *zoneEncryptionKeyService) ActivateZoneEncryptionKey(ctx context.Context, in *entity.ActivateZoneEncryptionKey) (*entity.ActivateZoneEncryptionKey, error) {
	startedAt := time.Now()
	out, err := s.repo.ActivateZoneEncryptionKey(ctx, in)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "ActivateZoneEncryptionKey", metrics.OutcomeFailure, time.Since(startedAt), err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}
	metrics.Downstream(ctx, metrics.KindRepo, "ActivateZoneEncryptionKey", metrics.OutcomeSuccess, time.Since(startedAt), nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return out, nil
}

func (s *zoneEncryptionKeyService) RetireZoneEncryptionKey(ctx context.Context, in *entity.RetireZoneEncryptionKey) (*entity.RetireZoneEncryptionKey, error) {
	startedAt := time.Now()
	out, err := s.repo.RetireZoneEncryptionKey(ctx, in)
	if err != nil {
		metrics.Downstream(ctx, metrics.KindRepo, "RetireZoneEncryptionKey", metrics.OutcomeFailure, time.Since(startedAt), err)
		metrics.ServiceCall(ctx, metrics.OutcomeFailure)
		return nil, err
	}
	metrics.Downstream(ctx, metrics.KindRepo, "RetireZoneEncryptionKey", metrics.OutcomeSuccess, time.Since(startedAt), nil)
	metrics.ServiceCall(ctx, metrics.OutcomeSuccess)
	return out, nil
}
