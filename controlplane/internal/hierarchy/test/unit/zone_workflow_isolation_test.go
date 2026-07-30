package unit

import (
	"context"
	"errors"
	"net"
	"testing"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchySvcImpl "controlplane/internal/hierarchy/service"
	"controlplane/internal/observability"

	goredis "github.com/redis/go-redis/v9"
)

type zoneWorkflowRepository struct {
	listCatalogCalls  int
	resolveCalls      int
	getDetailCalls    int
	updateStatusCalls int
}

func (r *zoneWorkflowRepository) ListZones(context.Context, *hierarchyEntity.ListZones) ([]hierarchyEntity.ListZones, error) {
	return nil, nil
}

func (r *zoneWorkflowRepository) ListZoneCatalog(context.Context, *hierarchyEntity.ListZoneCatalog) ([]hierarchyEntity.ListZoneCatalog, error) {
	r.listCatalogCalls++
	return nil, nil
}

func (r *zoneWorkflowRepository) ResolveZoneByCode(_ context.Context, in *hierarchyEntity.ResolveZoneByCode) (*hierarchyEntity.ResolveZoneByCode, error) {
	r.resolveCalls++
	return &hierarchyEntity.ResolveZoneByCode{Code: in.Code, Found: true}, nil
}

func (r *zoneWorkflowRepository) CreateZone(context.Context, *hierarchyEntity.CreateZone) (*hierarchyEntity.CreateZone, error) {
	return nil, nil
}

func (r *zoneWorkflowRepository) GetZoneDetail(context.Context, *hierarchyEntity.GetZoneDetail) ([]hierarchyEntity.GetZoneDetail, error) {
	r.getDetailCalls++
	return nil, nil
}

func (r *zoneWorkflowRepository) UpdateZoneStatus(_ context.Context, in *hierarchyEntity.UpdateZoneStatus) (*hierarchyEntity.UpdateZoneStatus, error) {
	r.updateStatusCalls++
	return &hierarchyEntity.UpdateZoneStatus{
		ZoneID: in.ZoneID, Status: in.Status, ZoneCode: "zone-1", ZoneName: "Zone 1", StateChanged: true,
	}, nil
}

func (r *zoneWorkflowRepository) DeleteZone(context.Context, *hierarchyEntity.DeleteZone) (*hierarchyEntity.DeleteZone, error) {
	return nil, nil
}

func (r *zoneWorkflowRepository) UpdateZoneService(context.Context, *hierarchyEntity.UpdateZoneService) (*hierarchyEntity.UpdateZoneService, error) {
	return nil, nil
}

func TestResolveZoneByCodeDoesNotCallListWorkflow(t *testing.T) {
	redisClient := goredis.NewClient(&goredis.Options{
		MaxRetries: -1,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("redis unavailable")
		},
	})
	t.Cleanup(func() { _ = redisClient.Close() })
	repository := &zoneWorkflowRepository{}
	service := hierarchySvcImpl.NewZoneService(repository, redisClient, observability.NewNoopWorkflowRecorder())

	result, err := service.ResolveZoneByCode(context.Background(), &hierarchyEntity.ResolveZoneByCode{Code: "zone-1"})
	if err != nil {
		t.Fatalf("resolve zone: %v", err)
	}
	if !result.Found || repository.resolveCalls != 1 || repository.listCatalogCalls != 0 {
		t.Fatalf("resolve workflow crossed into list workflow: resolve=%d list=%d", repository.resolveCalls, repository.listCatalogCalls)
	}
}

func TestUpdateZoneStatusDoesNotReadAnotherWorkflowForInvalidation(t *testing.T) {
	redisClient := goredis.NewClient(&goredis.Options{
		MaxRetries: -1,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("redis unavailable")
		},
	})
	t.Cleanup(func() { _ = redisClient.Close() })
	repository := &zoneWorkflowRepository{}
	service := hierarchySvcImpl.NewZoneService(repository, redisClient, observability.NewNoopWorkflowRecorder())

	_, err := service.UpdateZoneStatus(context.Background(), &hierarchyEntity.UpdateZoneStatus{Status: hierarchyEntity.ZoneStatusActive})
	if err != nil {
		t.Fatalf("update zone status: %v", err)
	}
	if repository.updateStatusCalls != 1 || repository.getDetailCalls != 0 {
		t.Fatalf("status workflow crossed into detail workflow: update=%d detail=%d", repository.updateStatusCalls, repository.getDetailCalls)
	}
}
