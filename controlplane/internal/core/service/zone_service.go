package coreSvcImpl

import (
	"context"
	"strings"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreErrorx "controlplane/internal/core/errorx"

	"github.com/google/uuid"
)

type ZoneService struct {
	repo coreRepoInterface.ZoneRepository
}

func NewZoneService(repo coreRepoInterface.ZoneRepository) *ZoneService {
	return &ZoneService{repo: repo}
}

func (s *ZoneService) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	if s == nil || s.repo == nil {
		return nil, coreErrorx.ErrZoneInvalidInput
	}
	return s.repo.ListZones(ctx)
}

func (s *ZoneService) CreateZone(ctx context.Context, code, name string, status *coreEntity.ZoneStatus) (*coreEntity.Zone, error) {
	if s == nil || s.repo == nil {
		return nil, coreErrorx.ErrZoneInvalidInput
	}
	code = strings.ToLower(strings.TrimSpace(code))
	name = strings.TrimSpace(name)
	if code == "" || name == "" {
		return nil, coreErrorx.ErrZoneInvalidInput
	}

	zoneStatus := coreEntity.ZoneStatusActive
	if status != nil {
		zoneStatus = *status
	}
	if !isValidZoneStatus(zoneStatus) {
		return nil, coreErrorx.ErrZoneInvalidInput
	}

	now := time.Now().UTC()
	zoneID, zoneIDErr := uuid.NewV7()
	if zoneIDErr != nil {
		zoneID = uuid.New()
	}
	zone := coreEntity.Zone{
		ID:        zoneID.String(),
		Code:      code,
		Name:      name,
		Status:    zoneStatus,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.CreateZone(ctx, zone); err != nil {
		return nil, err
	}
	return &zone, nil
}

func (s *ZoneService) UpdateZoneStatus(ctx context.Context, zoneID string, toStatus coreEntity.ZoneStatus) (*coreEntity.Zone, error) {
	if s == nil || s.repo == nil {
		return nil, coreErrorx.ErrZoneInvalidInput
	}
	parsedID, err := uuid.Parse(strings.TrimSpace(zoneID))
	if err != nil || !isValidZoneStatus(toStatus) {
		return nil, coreErrorx.ErrZoneInvalidInput
	}

	zone, err := s.repo.GetZoneByID(ctx, parsedID)
	if err != nil {
		return nil, err
	}
	if zone == nil {
		return nil, coreErrorx.ErrZoneNotFound
	}
	if !canTransit(zone.Status, toStatus) {
		return nil, coreErrorx.ErrZoneInvalidTransition
	}
	if err := s.repo.UpdateZoneStatus(ctx, parsedID, toStatus); err != nil {
		return nil, err
	}
	return s.repo.GetZoneByID(ctx, parsedID)
}

func (s *ZoneService) DeleteZone(ctx context.Context, zoneID string) error {
	if s == nil || s.repo == nil {
		return coreErrorx.ErrZoneInvalidInput
	}
	parsedID, err := uuid.Parse(strings.TrimSpace(zoneID))
	if err != nil {
		return coreErrorx.ErrZoneInvalidInput
	}

	zone, err := s.repo.GetZoneByID(ctx, parsedID)
	if err != nil {
		return err
	}
	if zone == nil {
		return coreErrorx.ErrZoneNotFound
	}
	if zone.Status != coreEntity.ZoneStatusDisabled {
		return coreErrorx.ErrZoneDeletePreconditionFailed
	}
	hasNodes, err := s.repo.HasDataplaneNodesByZone(ctx, parsedID)
	if err != nil {
		return err
	}
	if hasNodes {
		return coreErrorx.ErrZoneDeletePreconditionFailed
	}
	hasEnabledSvc, err := s.repo.HasEnabledZoneServicesByZone(ctx, parsedID)
	if err != nil {
		return err
	}
	if hasEnabledSvc {
		return coreErrorx.ErrZoneDeletePreconditionFailed
	}
	return s.repo.DeleteZone(ctx, parsedID)
}

func isValidZoneStatus(value coreEntity.ZoneStatus) bool {
	switch value {
	case coreEntity.ZoneStatusActive, coreEntity.ZoneStatusDraining,
		coreEntity.ZoneStatusMaintenance, coreEntity.ZoneStatusDisabled:
		return true
	default:
		return false
	}
}

func canTransit(from, to coreEntity.ZoneStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case coreEntity.ZoneStatusActive:
		return to == coreEntity.ZoneStatusDraining || to == coreEntity.ZoneStatusMaintenance || to == coreEntity.ZoneStatusDisabled
	case coreEntity.ZoneStatusDraining:
		return to == coreEntity.ZoneStatusActive || to == coreEntity.ZoneStatusMaintenance || to == coreEntity.ZoneStatusDisabled
	case coreEntity.ZoneStatusMaintenance:
		return to == coreEntity.ZoneStatusActive || to == coreEntity.ZoneStatusDisabled
	case coreEntity.ZoneStatusDisabled:
		return to == coreEntity.ZoneStatusActive
	default:
		return false
	}
}

func (s *ZoneService) ListZoneServices(ctx context.Context, zoneID string) ([]coreEntity.ZoneService, error) {
	if s == nil || s.repo == nil {
		return nil, coreErrorx.ErrZoneServiceInvalidInput
	}
	parsedID, err := uuid.Parse(strings.TrimSpace(zoneID))
	if err != nil {
		return nil, coreErrorx.ErrZoneServiceInvalidInput
	}
	zone, err := s.repo.GetZoneByID(ctx, parsedID)
	if err != nil {
		return nil, err
	}
	if zone == nil {
		return nil, coreErrorx.ErrZoneServiceZoneNotFound
	}
	return s.repo.ListZoneServicesByZoneID(ctx, parsedID)
}

func (s *ZoneService) UpsertZoneService(ctx context.Context, zoneID string, serviceType string, enabled bool) (*coreEntity.ZoneService, error) {
	if s == nil || s.repo == nil {
		return nil, coreErrorx.ErrZoneServiceInvalidInput
	}
	parsedID, err := uuid.Parse(strings.TrimSpace(zoneID))
	if err != nil {
		return nil, coreErrorx.ErrZoneServiceInvalidInput
	}
	zone, err := s.repo.GetZoneByID(ctx, parsedID)
	if err != nil {
		return nil, err
	}
	if zone == nil {
		return nil, coreErrorx.ErrZoneServiceZoneNotFound
	}
	if zone.Status != coreEntity.ZoneStatusMaintenance {
		return nil, coreErrorx.ErrZoneServiceStateConflict
	}
	serviceType = strings.ToLower(strings.TrimSpace(serviceType))
	typed := coreEntity.ZoneServiceType(serviceType)
	switch typed {
	case coreEntity.ZoneServiceTypeMail, coreEntity.ZoneServiceTypeHypervisor, coreEntity.ZoneServiceTypeK8s, coreEntity.ZoneServiceTypeAI:
	default:
		return nil, coreErrorx.ErrZoneServiceInvalidType
	}
	return s.repo.UpsertZoneServiceByZoneAndType(ctx, parsedID, typed, enabled)
}
