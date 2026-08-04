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

type RbacTenantService struct {
	repo    iamRepoInterface.RbacTenantRepository
	metrics observability.WorkflowRecorder
}

func NewRbacTenantService(repo iamRepoInterface.RbacTenantRepository, metrics observability.WorkflowRecorder) iamSvcInterface.RbacTenantService {
	return &RbacTenantService{repo: repo, metrics: metrics}
}

func (s *RbacTenantService) ListTenantRoles(ctx context.Context, in *iamEntity.ListTenantRoles) (out []iamEntity.ListTenantRoles, err error) {
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
	return s.repo.ListTenantRoles(ctx, in)
}

func (s *RbacTenantService) CreateTenantRole(ctx context.Context, in *iamEntity.CreateTenantRole) (out *iamEntity.CreateTenantRole, err error) {
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
	in.Version = 1
	in.CreatedAt = time.Now().UTC()
	return s.repo.CreateTenantRole(ctx, in)
}

func (s *RbacTenantService) ResolveTenantAccess(ctx context.Context, in *iamEntity.ResolveTenantAccess) (out *iamEntity.ResolveTenantAccess, err error) {
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
