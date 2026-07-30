package hierarchySvcImpl

import (
	"context"
	"fmt"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyMetrics "controlplane/internal/hierarchy/metrics"

	"github.com/google/uuid"
)

type TenantService struct {
	repo                hierarchyRepoInterface.TenantRepository
	notifyBillingOutbox func()
}

func NewTenantService(repo hierarchyRepoInterface.TenantRepository) hierarchySvcInterface.TenantService {
	return &TenantService{repo: repo}
}

// SetBillingOutboxNotifier is wired before readiness. The notification is only
// a latency hint; the transactional outbox remains the recovery boundary.
func (s *TenantService) SetBillingOutboxNotifier(notify func()) {
	s.notifyBillingOutbox = notify
}

func (s *TenantService) CreateTenant(ctx context.Context, in *hierarchyEntity.CreateTenant) (*hierarchyEntity.CreateTenant, error) {
	tenantID, err := uuid.NewV7()
	if err != nil {
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, fmt.Errorf("generate tenant id: %w", err)
	}
	now := time.Now().UTC()
	in.ID = tenantID
	in.Status = hierarchyEntity.TenantStatusActive
	in.CreatedAt = now
	in.UpdatedAt = now

	startedAt := time.Now()
	out, err := s.repo.CreateTenant(ctx, in)
	if err != nil {
		hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "CreateTenant", hierarchyMetrics.OutcomeFailure, time.Since(startedAt), err)
		hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeFailure)
		return nil, err
	}
	hierarchyMetrics.Downstream(ctx, hierarchyMetrics.KindRepo, "CreateTenant", hierarchyMetrics.OutcomeSuccess, time.Since(startedAt), nil)
	hierarchyMetrics.ServiceCall(ctx, hierarchyMetrics.OutcomeSuccess)
	s.notifyBillingOutbox()
	return out, nil
}
