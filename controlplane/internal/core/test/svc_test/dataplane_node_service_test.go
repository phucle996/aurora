package svc_test

import (
	"context"
	"testing"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreSvcImpl "controlplane/internal/core/service"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// Mock repository and cache for unit testing
type mockDataplaneNodeRepo struct {
	clusters map[string]coreEntity.DataplaneNode
	status   map[string]coreEntity.DataplaneNodeStatus
}

func (m *mockDataplaneNodeRepo) RegisterCluster(ctx context.Context, cluster coreEntity.DataplaneNode) error {
	m.clusters[cluster.ZoneID] = cluster
	m.clusters[cluster.ID] = cluster
	return nil
}

func (m *mockDataplaneNodeRepo) UpdateClusterStatus(ctx context.Context, id uuid.UUID, status coreEntity.DataplaneNodeStatus) error {
	m.status[id.String()] = status
	if c, ok := m.clusters[id.String()]; ok {
		c.Status = status
		c.UpdatedAt = time.Now().UTC()
		m.clusters[id.String()] = c
		m.clusters[c.ZoneID] = c
	}
	return nil
}

func (m *mockDataplaneNodeRepo) GetCluster(ctx context.Context, id uuid.UUID) (*coreEntity.DataplaneNode, error) {
	if c, ok := m.clusters[id.String()]; ok {
		return &c, nil
	}
	return nil, nil
}

func (m *mockDataplaneNodeRepo) GetClusterByZone(ctx context.Context, zoneID uuid.UUID) (*coreEntity.DataplaneNode, error) {
	if c, ok := m.clusters[zoneID.String()]; ok {
		return &c, nil
	}
	return nil, nil
}

func (m *mockDataplaneNodeRepo) ListReadyClusters(ctx context.Context) ([]coreEntity.DataplaneNode, error) {
	var out []coreEntity.DataplaneNode
	for _, c := range m.clusters {
		if c.Status == coreEntity.DataplaneNodeStatusReady {
			out = append(out, c)
		}
	}
	return out, nil
}

type mockDataplaneCacheForService struct {
	leases  map[string]bool
	metrics map[string]map[string]interface{}
}

func (m *mockDataplaneCacheForService) AcquireLease(ctx context.Context, zoneID string, ttl time.Duration) error {
	m.leases[zoneID] = true
	return nil
}

func (m *mockDataplaneCacheForService) CheckLeaseExists(ctx context.Context, zoneID string) (bool, error) {
	return m.leases[zoneID], nil
}

func (m *mockDataplaneCacheForService) SaveClusterMetrics(ctx context.Context, clusterID string, metrics map[string]interface{}, ttl time.Duration) error {
	m.metrics[clusterID] = metrics
	return nil
}

func (m *mockDataplaneCacheForService) GetClusterMetrics(ctx context.Context, clusterID string) (map[string]interface{}, error) {
	return m.metrics[clusterID], nil
}

func (m *mockDataplaneCacheForService) Subscribe(ctx context.Context, channel string) *goredis.PubSub {
	return nil
}

func (m *mockDataplaneCacheForService) GetActiveNodes(ctx context.Context, zoneID string) ([]string, error) {
	return []string{"mock-node-1"}, nil
}

func (m *mockDataplaneCacheForService) CheckNodeLiveness(ctx context.Context, zoneID string, hostname string) (bool, error) {
	return true, nil
}

func (m *mockDataplaneCacheForService) AcquireSalvageLock(ctx context.Context, zoneID string, hostname string) (bool, error) {
	return true, nil
}

func (m *mockDataplaneCacheForService) RemoveNodeFromActivePool(ctx context.Context, zoneID string, hostname string) error {
	return nil
}

type mockZoneRepoForService struct {
	zones    map[uuid.UUID]coreEntity.Zone
	services map[uuid.UUID][]coreEntity.ZoneService
}

func (m *mockZoneRepoForService) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	return nil, nil
}
func (m *mockZoneRepoForService) CreateZone(ctx context.Context, zone coreEntity.Zone) error {
	return nil
}
func (m *mockZoneRepoForService) GetZoneByID(ctx context.Context, id uuid.UUID) (*coreEntity.Zone, error) {
	if z, ok := m.zones[id]; ok {
		return &z, nil
	}
	return nil, nil
}
func (m *mockZoneRepoForService) UpdateZoneStatus(ctx context.Context, id uuid.UUID, status coreEntity.ZoneStatus) error {
	return nil
}
func (m *mockZoneRepoForService) DeleteZone(ctx context.Context, id uuid.UUID) error {
	return nil
}
func (m *mockZoneRepoForService) HasDataplaneNodesByZone(ctx context.Context, zoneID uuid.UUID) (bool, error) {
	return false, nil
}
func (m *mockZoneRepoForService) HasEnabledZoneServicesByZone(ctx context.Context, zoneID uuid.UUID) (bool, error) {
	return false, nil
}
func (m *mockZoneRepoForService) ListZoneServicesByZoneID(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error) {
	return m.services[zoneID], nil
}
func (m *mockZoneRepoForService) UpsertZoneServiceByZoneAndType(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error) {
	return nil, nil
}

func TestVerifyClusterStatus_FastPath(t *testing.T) {
	zoneID, _ := uuid.NewV7()

	repo := &mockDataplaneNodeRepo{
		clusters: make(map[string]coreEntity.DataplaneNode),
		status:   make(map[string]coreEntity.DataplaneNodeStatus),
	}
	cache := &mockDataplaneCacheForService{
		leases:  make(map[string]bool),
		metrics: make(map[string]map[string]interface{}),
	}
	zoneRepo := &mockZoneRepoForService{
		zones:    make(map[uuid.UUID]coreEntity.Zone),
		services: make(map[uuid.UUID][]coreEntity.ZoneService),
	}

	// 1. Setup Lease on Cache (simulating active heartbeat)
	cache.leases[zoneID.String()] = true

	svc := coreSvcImpl.NewDataplaneNodeService(repo, cache, zoneRepo)

	// 2. Call VerifyClusterStatus
	status, err := svc.VerifyClusterStatus(context.Background(), zoneID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status != string(coreEntity.DataplaneNodeStatusReady) {
		t.Errorf("expected status 'ready', got '%s'", status)
	}

	// Verify no PostgreSQL lookup or mutations happened since it hit fast path
	if len(repo.status) > 0 {
		t.Errorf("expected no postgres mutations, but status was updated: %v", repo.status)
	}
}

func TestVerifyClusterStatus_SlowPath_LeaseExpired(t *testing.T) {
	zoneID, _ := uuid.NewV7()
	clusterID, _ := uuid.NewV7()

	repo := &mockDataplaneNodeRepo{
		clusters: make(map[string]coreEntity.DataplaneNode),
		status:   make(map[string]coreEntity.DataplaneNodeStatus),
	}
	cache := &mockDataplaneCacheForService{
		leases:  make(map[string]bool),
		metrics: make(map[string]map[string]interface{}),
	}
	zoneRepo := &mockZoneRepoForService{
		zones:    make(map[uuid.UUID]coreEntity.Zone),
		services: make(map[uuid.UUID][]coreEntity.ZoneService),
	}

	// Setup cluster in PostgreSQL with 'ready' status
	cluster := coreEntity.DataplaneNode{
		ID:        clusterID.String(),
		Status:    coreEntity.DataplaneNodeStatusReady,
		ZoneID:    zoneID.String(),
		Endpoint:  "dp-lb.zone-a.internal:9000",
		UpdatedAt: time.Now().UTC().Add(-35 * time.Second), // stale
	}
	repo.clusters[zoneID.String()] = cluster
	repo.clusters[clusterID.String()] = cluster

	// Simulation: Lease key on Redis does not exist (expired)
	cache.leases[zoneID.String()] = false

	svc := coreSvcImpl.NewDataplaneNodeService(repo, cache, zoneRepo)

	// Call VerifyClusterStatus
	status, err := svc.VerifyClusterStatus(context.Background(), zoneID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status != string(coreEntity.DataplaneNodeStatusFailed) {
		t.Errorf("expected status 'failed', got '%s'", status)
	}

	// Verify PostgreSQL status was updated to failed instantly (Fast Probing triggered)
	updatedStatus := repo.status[clusterID.String()]
	if updatedStatus != coreEntity.DataplaneNodeStatusFailed {
		t.Errorf("expected postgres status updated to 'failed', got '%s'", updatedStatus)
	}
}
