package hierarchySvcImpl

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

type zoneEncryptionKeyService struct {
	repo    hierarchyRepoInterface.ZoneEncryptionKeyRepository
	metrics observability.WorkflowRecorder
}

func NewZoneEncryptionKeyService(repo hierarchyRepoInterface.ZoneEncryptionKeyRepository, metrics observability.WorkflowRecorder) hierarchySvcInterface.ZoneEncryptionKeyService {
	return &zoneEncryptionKeyService{repo: repo, metrics: metrics}
}

func (s *zoneEncryptionKeyService) RegisterZoneEncryptionKey(ctx context.Context, in *hierarchyEntity.RegisterZoneEncryptionKey) (*hierarchyEntity.RegisterZoneEncryptionKey, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	// [COMMENT]: Resource identity and derived cryptographic metadata are owned
	// by the service. A retried internal workflow keeps any preassigned UUID.
	if in.ID == uuid.Nil {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		in.ID = id
	}
	fingerprint := sha256.Sum256(in.PublicKey)
	in.Fingerprint = fingerprint[:]
	in.Algorithm = hierarchyEntity.ZoneEncryptionKeyAlgorithm
	in.Status = hierarchyEntity.ZoneEncryptionKeyStatusStaged

	out, err := s.repo.RegisterZoneEncryptionKey(ctx, in)
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hierarchyTaxonomy.ErrConflict):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		}
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return out, nil
}

func (s *zoneEncryptionKeyService) ListZoneEncryptionKeys(ctx context.Context, in *hierarchyEntity.ListZoneEncryptionKeys) ([]hierarchyEntity.ListZoneEncryptionKeys, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	out, err := s.repo.ListZoneEncryptionKeys(ctx, in)
	if err != nil {
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return out, nil
}

func (s *zoneEncryptionKeyService) ActivateZoneEncryptionKey(ctx context.Context, in *hierarchyEntity.ActivateZoneEncryptionKey) (*hierarchyEntity.ActivateZoneEncryptionKey, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	out, err := s.repo.ActivateZoneEncryptionKey(ctx, in)
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, hierarchyTaxonomy.ErrConflict) {
			result, reason = observability.ResultRejected, observability.ReasonConflict
		} else if errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed) {
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		} else if errors.Is(err, hierarchyTaxonomy.ErrInvalidTransition) {
			result, reason = observability.ResultRejected, observability.ReasonInvalidTransition
		}
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return out, nil
}

func (s *zoneEncryptionKeyService) RetireZoneEncryptionKey(ctx context.Context, in *hierarchyEntity.RetireZoneEncryptionKey) (*hierarchyEntity.RetireZoneEncryptionKey, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	out, err := s.repo.RetireZoneEncryptionKey(ctx, in)
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, hierarchyTaxonomy.ErrConflict) {
			result, reason = observability.ResultRejected, observability.ReasonConflict
		} else if errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed) {
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		} else if errors.Is(err, hierarchyTaxonomy.ErrInvalidTransition) {
			result, reason = observability.ResultRejected, observability.ReasonInvalidTransition
		}
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return out, nil
}
