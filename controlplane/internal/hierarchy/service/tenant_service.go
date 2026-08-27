package hierarchySvcImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

type TenantService struct {
	repo                        hierarchyRepoInterface.TenantRepository
	notifyTenantWalletProvision func()
	metrics                     observability.WorkflowRecorder
}

func NewTenantService(
	repo hierarchyRepoInterface.TenantRepository,
	metrics observability.WorkflowRecorder,
	notifyTenantWalletProvision func(),
) hierarchySvcInterface.TenantService {
	return &TenantService{repo: repo, metrics: metrics, notifyTenantWalletProvision: notifyTenantWalletProvision}
}

func (s *TenantService) CreateTenant(ctx context.Context, in *hierarchyEntity.CreateTenant) (*hierarchyEntity.CreateTenant, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	tenantID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate tenant id: %w", err)
	}
	now := time.Now().UTC()
	in.ID = tenantID
	ownerMembershipID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate owner membership id: %w", err)
	}
	tenantRootRoleID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate tenant root role id: %w", err)
	}
	membershipRoleID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate owner membership role id: %w", err)
	}
	domainID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate tenant domain id: %w", err)
	}
	in.OwnerMembershipID = ownerMembershipID
	in.TenantRootRoleID = tenantRootRoleID
	in.MembershipRoleID = membershipRoleID
	in.DomainID = domainID
	in.Status = hierarchyEntity.TenantStatusActive
	in.CreatedAt = now
	in.UpdatedAt = now

	out, err := s.repo.CreateTenant(ctx, in)
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrAlreadyExists) {
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		}
		return nil, err
	}
	if s.notifyTenantWalletProvision != nil {
		s.notifyTenantWalletProvision()
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return out, nil
}

func (s *TenantService) ListTenantsForUser(ctx context.Context, userID uuid.UUID) (out []hierarchyEntity.TenantCatalogItem, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListTenantsForUser(ctx, userID)
}
