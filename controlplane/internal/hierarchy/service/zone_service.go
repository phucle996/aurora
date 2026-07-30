package hierarchySvcImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	hierarchyproto "controlplane/internal/hierarchy/transport/proto"
	"controlplane/internal/observability"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type ZoneService struct {
	repo    hierarchyRepoInterface.ZoneRepository
	rds     *goredis.Client
	metrics observability.WorkflowRecorder
}

func NewZoneService(repo hierarchyRepoInterface.ZoneRepository, rds *goredis.Client, metrics observability.WorkflowRecorder) hierarchySvcInterface.ZoneService {
	return &ZoneService{repo: repo, rds: rds, metrics: metrics}
}

func (s *ZoneService) ListZones(ctx context.Context, in *hierarchyEntity.ListZones) ([]hierarchyEntity.ListZones, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	items, err := s.repo.ListZones(ctx, in)
	if err != nil {
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return items, nil
}

func (s *ZoneService) ListZoneCatalog(ctx context.Context, in *hierarchyEntity.ListZoneCatalog) ([]hierarchyEntity.ListZoneCatalog, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	items, err := s.repo.ListZoneCatalog(ctx, in)
	if err != nil {
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return items, nil
}

func (s *ZoneService) ResolveZoneByCode(ctx context.Context, in *hierarchyEntity.ResolveZoneByCode) (*hierarchyEntity.ResolveZoneByCode, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	out, err := s.repo.ResolveZoneByCode(ctx, in)
	if err != nil {
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return out, nil
}

func (s *ZoneService) CreateZone(ctx context.Context, in *hierarchyEntity.CreateZone) (*hierarchyEntity.CreateZone, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	zoneID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate zone id: %w", err)
	}
	hypervisorID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate hypervisor service id: %w", err)
	}
	storageID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate storage service id: %w", err)
	}
	mailID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate mail service id: %w", err)
	}
	kubernetesID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate kubernetes service id: %w", err)
	}
	aiID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate AI service id: %w", err)
	}
	databaseID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate database service id: %w", err)
	}
	managedServiceID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate managed service id: %w", err)
	}

	now := time.Now().UTC()
	in.ID = zoneID
	in.Status = hierarchyEntity.ZoneStatusPlanned
	in.HypervisorServiceID = hypervisorID
	in.StorageServiceID = storageID
	in.MailServiceID = mailID
	in.KubernetesServiceID = kubernetesID
	in.AIServiceID = aiID
	in.DatabaseServiceID = databaseID
	in.ManagedServiceID = managedServiceID
	in.CreatedAt = now
	in.UpdatedAt = now

	out, err := s.repo.CreateZone(ctx, in)
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrAlreadyExists) {
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		}
		return nil, err
	}

	// Shared Redis is rebuildable soft state. PostgreSQL has already committed,
	// so cache and fanout failures cannot reverse the business outcome.
	redisKey := fmt.Sprintf("zone:code:%s", strings.ToLower(strings.TrimSpace(out.Code)))
	redisValue := fmt.Sprintf("%s:%s", out.ID, out.Status)
	_ = s.rds.Set(ctx, redisKey, redisValue, 24*time.Hour).Err()
	event := &hierarchyproto.ZoneInvalidatedEvent{
		ZoneId: out.ID.String(), ZoneCode: out.Code, Status: string(out.Status), Name: out.Name,
	}
	if wire, marshalErr := proto.Marshal(event); marshalErr == nil {
		_ = s.rds.Publish(ctx, "hierarchy.zone.invalidated", wire).Err()
	}

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return out, nil
}

func (s *ZoneService) GetZoneDetail(ctx context.Context, in *hierarchyEntity.GetZoneDetail) ([]hierarchyEntity.GetZoneDetail, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	items, err := s.repo.GetZoneDetail(ctx, in)
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		return nil, err
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return items, nil
}

func (s *ZoneService) UpdateZoneStatus(ctx context.Context, in *hierarchyEntity.UpdateZoneStatus) (*hierarchyEntity.UpdateZoneStatus, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	switch in.Status {
	case hierarchyEntity.ZoneStatusPlanned:
		in.AllowedFrom = []hierarchyEntity.ZoneStatus{hierarchyEntity.ZoneStatusActive, hierarchyEntity.ZoneStatusDisabled, hierarchyEntity.ZoneStatusPlanned}
	case hierarchyEntity.ZoneStatusActive:
		in.AllowedFrom = []hierarchyEntity.ZoneStatus{hierarchyEntity.ZoneStatusPlanned, hierarchyEntity.ZoneStatusDraining, hierarchyEntity.ZoneStatusMaintenance, hierarchyEntity.ZoneStatusActive}
	case hierarchyEntity.ZoneStatusDraining:
		in.AllowedFrom = []hierarchyEntity.ZoneStatus{hierarchyEntity.ZoneStatusActive, hierarchyEntity.ZoneStatusDraining}
	case hierarchyEntity.ZoneStatusMaintenance:
		in.AllowedFrom = []hierarchyEntity.ZoneStatus{hierarchyEntity.ZoneStatusDraining, hierarchyEntity.ZoneStatusMaintenance}
	case hierarchyEntity.ZoneStatusDisabled:
		in.AllowedFrom = []hierarchyEntity.ZoneStatus{hierarchyEntity.ZoneStatusDraining, hierarchyEntity.ZoneStatusPlanned, hierarchyEntity.ZoneStatusDisabled}
	}

	out, err := s.repo.UpdateZoneStatus(ctx, in)
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, hierarchyTaxonomy.ErrInvalidTransition) {
			result, reason = observability.ResultRejected, observability.ReasonInvalidTransition
		}
		return nil, err
	}

	redisKey := fmt.Sprintf("zone:code:%s", strings.ToLower(strings.TrimSpace(out.ZoneCode)))
	redisValue := fmt.Sprintf("%s:%s", out.ZoneID, out.Status)
	_ = s.rds.Set(ctx, redisKey, redisValue, 24*time.Hour).Err()
	event := &hierarchyproto.ZoneInvalidatedEvent{
		ZoneId: out.ZoneID.String(), ZoneCode: out.ZoneCode, Status: string(out.Status), Name: out.ZoneName,
	}
	if wire, marshalErr := proto.Marshal(event); marshalErr == nil {
		_ = s.rds.Publish(ctx, "hierarchy.zone.invalidated", wire).Err()
	}

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return out, nil
}

func (s *ZoneService) DeleteZone(ctx context.Context, in *hierarchyEntity.DeleteZone) (*hierarchyEntity.DeleteZone, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	out, err := s.repo.DeleteZone(ctx, in)
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed) {
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		return nil, err
	}

	redisKey := fmt.Sprintf("zone:code:%s", strings.ToLower(strings.TrimSpace(out.ZoneCode)))
	_ = s.rds.Del(ctx, redisKey).Err()
	event := &hierarchyproto.ZoneInvalidatedEvent{
		ZoneId: out.ZoneID.String(), ZoneCode: out.ZoneCode, Deleted: true,
	}
	if wire, marshalErr := proto.Marshal(event); marshalErr == nil {
		_ = s.rds.Publish(ctx, "hierarchy.zone.invalidated", wire).Err()
	}

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return out, nil
}

func (s *ZoneService) UpdateZoneService(ctx context.Context, in *hierarchyEntity.UpdateZoneService) (*hierarchyEntity.UpdateZoneService, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	serviceID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate zone service id: %w", err)
	}
	in.ID = serviceID

	out, err := s.repo.UpdateZoneService(ctx, in)
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed) {
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		return nil, err
	}

	event := &hierarchyproto.ZoneInvalidatedEvent{
		ZoneId: out.ZoneID.String(), ZoneCode: out.ZoneCode, Status: string(out.ZoneStatus), Name: out.ZoneName,
	}
	if wire, marshalErr := proto.Marshal(event); marshalErr == nil {
		_ = s.rds.Publish(ctx, "hierarchy.zone.invalidated", wire).Err()
	}

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return out, nil
}
