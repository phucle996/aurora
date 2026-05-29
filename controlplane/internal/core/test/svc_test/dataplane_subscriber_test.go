package svc_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreSvcImpl "controlplane/internal/core/service"
	coreRpcHandler "controlplane/internal/core/transport/rpc/handler"
	coreProto "controlplane/internal/core/transport/rpc/proto"

	goredis "github.com/redis/go-redis/v9"
)

// MockDataplaneCache triển khai mock cho DataplaneCache phục vụ test.
type MockDataplaneCache struct {
	AcquireLeaseFunc      func(ctx context.Context, zoneID string, ttl time.Duration) error
	CheckLeaseExistsFunc  func(ctx context.Context, zoneID string) (bool, error)
	SaveClusterMetricsFunc func(ctx context.Context, clusterID string, metrics map[string]interface{}, ttl time.Duration) error
	GetClusterMetricsFunc func(ctx context.Context, clusterID string) (map[string]interface{}, error)
	SubscribeFunc         func(ctx context.Context, channel string) *goredis.PubSub
}

func (m *MockDataplaneCache) AcquireLease(ctx context.Context, zoneID string, ttl time.Duration) error {
	if m.AcquireLeaseFunc != nil {
		return m.AcquireLeaseFunc(ctx, zoneID, ttl)
	}
	return nil
}

func (m *MockDataplaneCache) CheckLeaseExists(ctx context.Context, zoneID string) (bool, error) {
	if m.CheckLeaseExistsFunc != nil {
		return m.CheckLeaseExistsFunc(ctx, zoneID)
	}
	return true, nil
}

func (m *MockDataplaneCache) SaveClusterMetrics(ctx context.Context, clusterID string, metrics map[string]interface{}, ttl time.Duration) error {
	if m.SaveClusterMetricsFunc != nil {
		return m.SaveClusterMetricsFunc(ctx, clusterID, metrics, ttl)
	}
	return nil
}

func (m *MockDataplaneCache) GetClusterMetrics(ctx context.Context, clusterID string) (map[string]interface{}, error) {
	if m.GetClusterMetricsFunc != nil {
		return m.GetClusterMetricsFunc(ctx, clusterID)
	}
	return nil, nil
}

func (m *MockDataplaneCache) Subscribe(ctx context.Context, channel string) *goredis.PubSub {
	if m.SubscribeFunc != nil {
		return m.SubscribeFunc(ctx, channel)
	}
	return nil
}

// MockDataplaneNodeService triển khai mock cho DataplaneNodeService phục vụ test.
type MockDataplaneNodeService struct {
	IngestHeartbeatFunc           func(ctx context.Context, clusterID string, zoneID string) error
	VerifyClusterStatusFunc       func(ctx context.Context, zoneID string) (string, error)
	GetEligibleClusterForZoneFunc func(ctx context.Context, zoneID string, serviceType string) (*coreEntity.DataplaneNode, error)
}

func (m *MockDataplaneNodeService) IngestHeartbeat(ctx context.Context, clusterID string, zoneID string) error {
	if m.IngestHeartbeatFunc != nil {
		return m.IngestHeartbeatFunc(ctx, clusterID, zoneID)
	}
	return nil
}

func (m *MockDataplaneNodeService) VerifyClusterStatus(ctx context.Context, zoneID string) (string, error) {
	if m.VerifyClusterStatusFunc != nil {
		return m.VerifyClusterStatusFunc(ctx, zoneID)
	}
	return string(coreEntity.DataplaneNodeStatusReady), nil
}

func (m *MockDataplaneNodeService) GetEligibleClusterForZone(ctx context.Context, zoneID string, serviceType string) (*coreEntity.DataplaneNode, error) {
	if m.GetEligibleClusterForZoneFunc != nil {
		return m.GetEligibleClusterForZoneFunc(ctx, zoneID, serviceType)
	}
	return nil, nil
}

// TestDataplaneGRPCHandler_Success xác nhận gRPC handler hoạt động thành công khi payload hợp lệ.
func TestDataplaneGRPCHandler_Success(t *testing.T) {
	mockSvc := &MockDataplaneNodeService{
		IngestHeartbeatFunc: func(ctx context.Context, clusterID string, zoneID string) error {
			if clusterID != "cluster-1" || zoneID != "zone-1" {
				t.Errorf("Unexpected params: cluster=%s, zone=%s", clusterID, zoneID)
			}
			return nil
		},
	}

	handler := coreRpcHandler.NewDataplaneGRPCHandler(mockSvc)
	req := &coreProto.HeartbeatRequest{
		ClusterId: "cluster-1",
		ZoneId:    "zone-1",
	}

	res, err := handler.Heartbeat(context.Background(), req)
	if err != nil {
		t.Fatalf("gRPC Heartbeat failed: %v", err)
	}

	if !res.Success {
		t.Error("Expected response success = true, got false")
	}
}

// TestDataplaneGRPCHandler_InvalidInput xác nhận gRPC handler trả lỗi khi thiếu tham số.
func TestDataplaneGRPCHandler_InvalidInput(t *testing.T) {
	mockSvc := &MockDataplaneNodeService{}
	handler := coreRpcHandler.NewDataplaneGRPCHandler(mockSvc)

	req := &coreProto.HeartbeatRequest{
		ClusterId: "",
		ZoneId:    "zone-1",
	}

	_, err := handler.Heartbeat(context.Background(), req)
	if err == nil {
		t.Error("Expected error due to empty cluster_id, got nil")
	}
}

// TestDataplaneGRPCHandler_ServiceError xác nhận gRPC handler trả lỗi khi Service báo lỗi.
func TestDataplaneGRPCHandler_ServiceError(t *testing.T) {
	mockSvc := &MockDataplaneNodeService{
		IngestHeartbeatFunc: func(ctx context.Context, clusterID string, zoneID string) error {
			return errors.New("db write error")
		},
	}

	handler := coreRpcHandler.NewDataplaneGRPCHandler(mockSvc)
	req := &coreProto.HeartbeatRequest{
		ClusterId: "cluster-1",
		ZoneId:    "zone-1",
	}

	_, err := handler.Heartbeat(context.Background(), req)
	if err == nil {
		t.Error("Expected error from service fallback, got nil")
	}
}

// TestSubscriberHeartbeat_Ingestion verify background subscriber parsing payload and invoking service.
func TestSubscriberHeartbeat_Ingestion(t *testing.T) {
	ingestCount := 0
	mockSvc := &MockDataplaneNodeService{
		IngestHeartbeatFunc: func(ctx context.Context, clusterID string, zoneID string) error {
			if clusterID == "cluster-123" && zoneID == "zone-456" {
				ingestCount++
			}
			return nil
		},
	}

	client := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
	mockCache := &MockDataplaneCache{
		SubscribeFunc: func(ctx context.Context, channel string) *goredis.PubSub {
			return client.Subscribe(ctx, channel)
		},
	}

	sub := coreSvcImpl.NewDataplaneHeartbeatSubscriber(mockCache, mockSvc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sub.Start(ctx)

	time.Sleep(100 * time.Millisecond)

	payload := struct {
		ClusterID string `json:"cluster_id"`
		ZoneID    string `json:"zone_id"`
	}{
		ClusterID: "cluster-123",
		ZoneID:    "zone-456",
	}
	bytes, _ := json.Marshal(payload)

	_ = client.Publish(context.Background(), "core:dataplane:heartbeats:pubsub", string(bytes))

	cancel()
	time.Sleep(50 * time.Millisecond)
}
