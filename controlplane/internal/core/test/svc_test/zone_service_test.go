package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreErrorx "controlplane/internal/core/errorx"
	coreSvcImpl "controlplane/internal/core/service"
)

type fakeZoneRepo struct {
	zone      *coreEntity.Zone
	zoneErr   error
	upsertRes *coreEntity.ZoneService
	upsertErr error
}

func (f *fakeZoneRepo) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	if f.zone == nil {
		return []coreEntity.Zone{}, nil
	}
	return []coreEntity.Zone{*f.zone}, nil
}
func (f *fakeZoneRepo) GetZoneCatalog(ctx context.Context) ([]coreEntity.ZoneCatalog, error) {
	return []coreEntity.ZoneCatalog{}, nil
}

func (f *fakeZoneRepo) CreateZone(ctx context.Context, zone coreEntity.Zone) error { return nil }
func (f *fakeZoneRepo) UpdateZoneStatus(ctx context.Context, id uuid.UUID, status coreEntity.ZoneStatus) error {
	return nil
}
func (f *fakeZoneRepo) DeleteZone(ctx context.Context, id uuid.UUID) error { return nil }
func (f *fakeZoneRepo) HasDataplaneNodesByZone(ctx context.Context, zoneID uuid.UUID) (bool, error) {
	return false, nil
}
func (f *fakeZoneRepo) HasEnabledZoneServicesByZone(ctx context.Context, zoneID uuid.UUID) (bool, error) {
	return false, nil
}
func (f *fakeZoneRepo) GetZoneByID(ctx context.Context, id uuid.UUID) (*coreEntity.Zone, error) {
	return f.zone, f.zoneErr
}
func (f *fakeZoneRepo) ListZoneServicesByZoneID(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error) {
	return []coreEntity.ZoneService{}, nil
}
func (f *fakeZoneRepo) UpsertZoneServiceByZoneAndType(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error) {
	if f.upsertErr != nil {
		return nil, f.upsertErr
	}
	if f.upsertRes != nil {
		return f.upsertRes, nil
	}
	return &coreEntity.ZoneService{ID: uuid.NewString(), ZoneID: zoneID.String(), ServiceType: serviceType, Enabled: enabled, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil
}

var _ coreRepoInterface.ZoneRepository = (*fakeZoneRepo)(nil)

func TestZoneServiceUpsertMaintenanceOnly(t *testing.T) {
	repo := &fakeZoneRepo{zone: &coreEntity.Zone{ID: uuid.NewString(), Status: coreEntity.ZoneStatusActive}}
	svc := coreSvcImpl.NewZoneService(repo, nil)
	_, err := svc.UpsertZoneService(context.Background(), uuid.MustParse(repo.zone.ID), "mail", true)
	if !errors.Is(err, coreErrorx.ErrZoneServiceStateConflict) {
		t.Fatalf("expected ErrZoneServiceStateConflict, got %v", err)
	}

	repo.zone.Status = coreEntity.ZoneStatusMaintenance
	_, err = svc.UpsertZoneService(context.Background(), uuid.MustParse(repo.zone.ID), "mail", true)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
}

func TestZoneServiceUpsertInvalidType(t *testing.T) {
	repo := &fakeZoneRepo{zone: &coreEntity.Zone{ID: uuid.NewString(), Status: coreEntity.ZoneStatusMaintenance}}
	svc := coreSvcImpl.NewZoneService(repo, nil)
	// Service layer does NOT validate service type — that is the handler/transport layer's responsibility.
	// Invalid type strings pass through to the repository; this test validates service-layer behavior only.
	_, err := svc.UpsertZoneService(context.Background(), uuid.MustParse(repo.zone.ID), "bad-type", true)
	if err != nil {
		t.Fatalf("expected nil err (service does not validate type), got %v", err)
	}
}
