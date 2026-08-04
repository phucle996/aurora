package hierarchySvcImpl

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

type TenantInvitationService struct {
	repo        hierarchyRepoInterface.TenantInvitationRepository
	cacheEngine *cacheengine.CacheRegistry
	metrics     observability.WorkflowRecorder
}

func NewTenantInvitationService(repo hierarchyRepoInterface.TenantInvitationRepository, cacheEngine *cacheengine.CacheRegistry, metrics observability.WorkflowRecorder) hierarchySvcInterface.TenantInvitationService {
	return &TenantInvitationService{repo: repo, cacheEngine: cacheEngine, metrics: metrics}
}

func (s *TenantInvitationService) CreateTenantInvitation(ctx context.Context, in *hierarchyEntity.CreateTenantInvitation) (out *hierarchyEntity.CreateTenantInvitation, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hierarchyTaxonomy.ErrAlreadyExists):
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	invitationID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate tenant invitation id: %w", err)
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate tenant invitation token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256(tokenBytes)
	now := time.Now().UTC()
	in.ID = invitationID
	in.WorkspaceID = uuid.Nil
	in.Token = token
	in.TokenHash = tokenHash[:]
	in.CreatedAt = now
	in.ExpiresAt = now.Add(6 * time.Hour)
	return s.repo.CreateTenantInvitation(ctx, in)
}

func (s *TenantInvitationService) PreviewTenantInvitation(ctx context.Context, in *hierarchyEntity.PreviewTenantInvitation) (out *hierarchyEntity.PreviewTenantInvitation, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.PreviewTenantInvitation(ctx, in)
}

func (s *TenantInvitationService) RevokeTenantInvitation(ctx context.Context, in *hierarchyEntity.RevokeTenantInvitation) (out *hierarchyEntity.RevokeTenantInvitation, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.RevokeTenantInvitation(ctx, in)
}

func (s *TenantInvitationService) JoinTenantInvitation(ctx context.Context, in *hierarchyEntity.JoinTenantInvitation) (out *hierarchyEntity.JoinTenantInvitation, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, hierarchyTaxonomy.ErrAlreadyExists):
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	membershipID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate tenant membership id: %w", err)
	}
	membershipRoleID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate tenant membership role id: %w", err)
	}
	in.MembershipID = membershipID
	in.MembershipRoleID = membershipRoleID
	out, err = s.repo.JoinTenantInvitation(ctx, in)
	if err != nil {
		return nil, err
	}

	cacheKey := "membership_role:" + in.UserID.String() + ":" + out.TenantID.String()
	s.cacheEngine.L1.Delete(cacheKey)
	// [COMMENT]: Joining adds authority, so a missed Pub/Sub invalidation can
	// only cause a bounded stale deny. The PostgreSQL commit remains successful;
	// returning an error here would make a consumed one-time link unretryable.
	_, _ = s.cacheEngine.Fanout.Publish(ctx, cacheKey, nil)
	return out, nil
}
