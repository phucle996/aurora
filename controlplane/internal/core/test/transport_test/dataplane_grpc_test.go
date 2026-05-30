package transport_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreRpcHandler "controlplane/internal/core/transport/rpc/handler"
	coreProto "controlplane/internal/core/transport/rpc/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// MockDataplaneNodeServiceForTransport mock service layer cho transport tests.
type MockDataplaneNodeServiceForTransport struct {
	IngestHeartbeatFunc           func(ctx context.Context, clusterID string, zoneID string) error
	VerifyClusterStatusFunc       func(ctx context.Context, zoneID string) (string, error)
	GetEligibleClusterForZoneFunc func(ctx context.Context, zoneID string, serviceType string) (*coreEntity.DataplaneNode, error)
	IngestFallbackHeartbeatFunc   func(ctx context.Context, hostname string, zoneID string) error
	CheckFallbackLivenessFunc     func(ctx context.Context, zoneID string, hostname string) bool
}

func (m *MockDataplaneNodeServiceForTransport) IngestHeartbeat(ctx context.Context, clusterID string, zoneID string) error {
	if m.IngestHeartbeatFunc != nil {
		return m.IngestHeartbeatFunc(ctx, clusterID, zoneID)
	}
	return nil
}

func (m *MockDataplaneNodeServiceForTransport) VerifyClusterStatus(ctx context.Context, zoneID string) (string, error) {
	if m.VerifyClusterStatusFunc != nil {
		return m.VerifyClusterStatusFunc(ctx, zoneID)
	}
	return string(coreEntity.DataplaneNodeStatusReady), nil
}

func (m *MockDataplaneNodeServiceForTransport) GetEligibleClusterForZone(ctx context.Context, zoneID string, serviceType string) (*coreEntity.DataplaneNode, error) {
	if m.GetEligibleClusterForZoneFunc != nil {
		return m.GetEligibleClusterForZoneFunc(ctx, zoneID, serviceType)
	}
	return nil, nil
}

func (m *MockDataplaneNodeServiceForTransport) IngestFallbackHeartbeat(ctx context.Context, hostname string, zoneID string) error {
	if m.IngestFallbackHeartbeatFunc != nil {
		return m.IngestFallbackHeartbeatFunc(ctx, hostname, zoneID)
	}
	return nil
}

func (m *MockDataplaneNodeServiceForTransport) CheckFallbackLiveness(ctx context.Context, zoneID string, hostname string) bool {
	if m.CheckFallbackLivenessFunc != nil {
		return m.CheckFallbackLivenessFunc(ctx, zoneID, hostname)
	}
	return true
}

// TestDataplaneGRPC_TransportLoop xác thực chu trình truyền nhận mạng gRPC hoàn chỉnh từ Client qua Socket tới Server.
func TestDataplaneGRPC_TransportLoop(t *testing.T) {
	// 1. Tạo mock service layer ghi nhận tham số cuộc gọi
	called := false
	mockSvc := &MockDataplaneNodeServiceForTransport{
		IngestHeartbeatFunc: func(ctx context.Context, clusterID string, zoneID string) error {
			if clusterID == "cluster-abc" && zoneID == "zone-xyz" {
				called = true
			}
			return nil
		},
	}

	// 2. Khởi tạo một tcp loopback listener ngẫu nhiên (cổng 0)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer lis.Close()

	// 3. Khởi chạy gRPC server và đăng ký DataplaneRegistryService Server
	grpcServer := grpc.NewServer()
	handler := coreRpcHandler.NewDataplaneGRPCHandler(mockSvc)
	coreProto.RegisterDataplaneRegistryServiceServer(grpcServer, handler)

	// Khởi động server bất đồng bộ
	go func() {
		if err := grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("Serve failed: %v", err)
		}
	}()
	defer grpcServer.GracefulStop()

	// 4. Khởi tạo gRPC Client kết nối thông qua socket TCP loopback vừa thiết lập
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	client := coreProto.NewDataplaneRegistryServiceClient(conn)

	// 5. Thực hiện cuộc gọi RPC Heartbeat thật qua mạng socket!
	req := &coreProto.HeartbeatRequest{
		ClusterId: "cluster-abc",
		ZoneId:    "zone-xyz",
	}

	res, err := client.Heartbeat(ctx, req)
	if err != nil {
		t.Fatalf("gRPC Heartbeat invocation failed: %v", err)
	}

	// 6. Kiểm chứng kết quả phản hồi từ gRPC server
	if !res.Success {
		t.Error("Expected heartbeat response success = true, got false")
	}

	if !called {
		t.Error("Service layer IngestHeartbeat was not invoked with correct parameters")
	}
}
