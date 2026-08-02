package hierarchySvcImpl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/cacheengine"
	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

const (
	zonePayloadKeyCacheNamespace = "hierarchy_zone_payload_key"
	zonePayloadKeyCacheTTL       = 30 * time.Second
	zonePayloadKeySafetyMargin   = 250 * time.Millisecond
)

type zoneEncryptionKeyService struct {
	repo        hierarchyRepoInterface.ZoneEncryptionKeyRepository
	cacheEngine *cacheengine.CacheRegistry
	fanout      cacheengine.FanoutPublisher
	metrics     observability.WorkflowRecorder
}

func NewZoneEncryptionKeyService(repo hierarchyRepoInterface.ZoneEncryptionKeyRepository, cacheEngine *cacheengine.CacheRegistry, fanout cacheengine.FanoutPublisher, metrics observability.WorkflowRecorder) hierarchySvcInterface.ZoneEncryptionKeyService {
	cacheengine.Register(cacheEngine, zonePayloadKeyCacheNamespace, zonePayloadKeyCacheTTL, func(ctx context.Context, param string) (*hierarchyEntity.ResolveZonePayloadKey, error) {
		// The registry transports a typed Zone UUID as a string cache parameter.
		// Parsing remains at this cache ingress; repository and security layers only
		// receive the typed workflow entity.
		zoneID, err := uuid.Parse(param)
		if err != nil {
			return nil, hierarchyTaxonomy.ErrPreconditionFailed
		}
		loadStartedAt := time.Now()
		out, err := repo.ResolveZonePayloadKey(ctx, &hierarchyEntity.ResolveZonePayloadKey{ZoneID: zoneID})
		if err != nil {
			return nil, err
		}
		// Cache Engine applies positive TTL jitter. A separate monotonic hard
		// deadline therefore fences key usability even if the RAM entry survives
		// beyond the PostgreSQL readiness lease.
		if out.ReadyFor <= zonePayloadKeySafetyMargin {
			return nil, hierarchyTaxonomy.ErrPreconditionFailed
		}
		out.UsableUntil = loadStartedAt.Add(out.ReadyFor - zonePayloadKeySafetyMargin)
		out.Available = true
		return out, nil
	})
	return &zoneEncryptionKeyService{repo: repo, cacheEngine: cacheEngine, fanout: fanout, metrics: metrics}
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
	// Rotation keeps the previous key DECRYPT_ONLY for in-flight commits, but
	// this replica must stop selecting the old ACTIVE head immediately. Other
	// replicas converge through the bounded hard readiness deadline.
	s.cacheEngine.L1.Delete(zonePayloadKeyCacheNamespace + ":" + in.ZoneID.String())
	// Publish only the cache-key invalidation after the repository commit. The
	// nil payload means public-key bytes never enter Redis. A lost Pub/Sub
	// message is safe because hard readiness expiry remains the fallback.
	_, _ = s.fanout.Publish(ctx, zonePayloadKeyCacheNamespace+":"+in.ZoneID.String(), nil)
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

func (s *zoneEncryptionKeyService) ResolveZonePayloadKey(ctx context.Context, in *hierarchyEntity.ResolveZonePayloadKey) (*hierarchyEntity.ResolveZonePayloadKey, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	cacheKey := zonePayloadKeyCacheNamespace + ":" + in.ZoneID.String()
	for attempt := 0; attempt < 2; attempt++ {
		cached, err := s.cacheEngine.GetOrLoad(ctx, zonePayloadKeyCacheNamespace, in.ZoneID.String())
		if err != nil {
			if errors.Is(err, hierarchyTaxonomy.ErrNotFound) || errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed) {
				result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
				// Expected unavailability is not cached and is represented explicitly so
				// the security boundary need not know Hierarchy taxonomy.
				return &hierarchyEntity.ResolveZonePayloadKey{ZoneID: in.ZoneID}, nil
			}
			return nil, err
		}
		out, ok := cached.(*hierarchyEntity.ResolveZonePayloadKey)
		if !ok {
			return nil, fmt.Errorf("hierarchy: invalid Zone payload key cache value")
		}
		if time.Now().Before(out.UsableUntil) {
			result, reason = observability.ResultSuccess, observability.ReasonNone
			// Cache values are immutable. Return a detached byte slice so a caller
			// cannot corrupt key material shared by concurrent sealing workflows.
			return &hierarchyEntity.ResolveZonePayloadKey{
				ZoneID:      out.ZoneID,
				KeyID:       out.KeyID,
				PublicKey:   bytes.Clone(out.PublicKey),
				UsableUntil: out.UsableUntil,
				Available:   true,
			}, nil
		}
		// Delete-before-reload cooperates with Cache Engine's tombstone and
		// singleflight fence, preventing an in-flight stale load from winning.
		s.cacheEngine.L1.Delete(cacheKey)
	}

	result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
	return &hierarchyEntity.ResolveZonePayloadKey{ZoneID: in.ZoneID}, nil
}
