package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

type TenantRbacService struct {
	repo    iamRepoInterface.TenantRbacRepository
	metrics observability.WorkflowRecorder
}

func NewTenantRbacService(repo iamRepoInterface.TenantRbacRepository, metrics observability.WorkflowRecorder) iamSvcInterface.TenantRbacService {
	return &TenantRbacService{repo: repo, metrics: metrics}
}

func (s *TenantRbacService) ListTenantRoles(ctx context.Context, in *iamEntity.ListTenantRoles) (out []iamEntity.ListTenantRoles, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		} else if errors.Is(err, iamTaxonomy.ErrConflict) {
			result, reason = observability.ResultRejected, observability.ReasonConflict
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListTenantRoles(ctx, in)
}

func (s *TenantRbacService) CreateTenantRole(ctx context.Context, in *iamEntity.CreateTenantRole) (out *iamEntity.CreateTenantRole, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, iamTaxonomy.ErrAlreadyExists):
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		case errors.Is(err, iamTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	roleID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate tenant role id: %w", err)
	}
	in.ID = roleID
	revisionID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate tenant role revision id: %w", err)
	}
	in.RevisionID = revisionID
	in.Version = 1
	in.CreatedAt = time.Now().UTC()
	return s.repo.CreateTenantRole(ctx, in)
}

func (s *TenantRbacService) GetTenantRole(ctx context.Context, in *iamEntity.GetTenantRole) (out *iamEntity.GetTenantRole, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.GetTenantRole(ctx, in)
}

func (s *TenantRbacService) CreateTenantRoleRevision(ctx context.Context, in *iamEntity.CreateTenantRoleRevision) (out *iamEntity.CreateTenantRoleRevision, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		case errors.Is(err, iamTaxonomy.ErrConflict):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		case errors.Is(err, iamTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	revisionID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate tenant role revision id: %w", err)
	}
	in.RevisionID = revisionID
	in.CreatedAt = time.Now().UTC()
	return s.repo.CreateTenantRoleRevision(ctx, in)
}

func (s *TenantRbacService) UpgradeTenantRoleAssignments(ctx context.Context, in *iamEntity.UpgradeTenantRoleAssignments) (out *iamEntity.UpgradeTenantRoleAssignments, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.UpgradeTenantRoleAssignments(ctx, in)
}

func (s *TenantRbacService) ResolveTenantAccess(ctx context.Context, in *iamEntity.ResolveTenantAccess) (out *iamEntity.ResolveTenantAccess, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ResolveTenantAccess(ctx, in)
}
