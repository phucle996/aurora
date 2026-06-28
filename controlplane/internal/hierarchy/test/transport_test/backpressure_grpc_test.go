package transport_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	coreRpcHandler "controlplane/internal/hierarchy/transport/rpc/handler"
	coreProto "controlplane/internal/hierarchy/transport/rpc/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// MockBackpressureServiceForTransport mock service layer cho transport tests.
type MockBackpressureServiceForTransport struct {
	ReportBackpressureFunc func(ctx context.Context, zoneID string, queueLen int64, pendingLen int64, congested bool, epoch int64, congestionRate float64) error
}

func (m *MockBackpressureServiceForTransport) ReportBackpressure(ctx context.Context, zoneID string, queueLen int64, pendingLen int64, congested bool, epoch int64, congestionRate float64) error {
	if m.ReportBackpressureFunc != nil {
		return m.ReportBackpressureFunc(ctx, zoneID, queueLen, pendingLen, congested, epoch, congestionRate)
	}
	return nil
}

// TestBackpressureGRPC_TransportLoop xác thực chu trình truyền nhận mạng gRPC hoàn chỉnh từ Client qua Socket tới Server.
func TestBackpressureGRPC_TransportLoop(t *testing.T) {
	// 1. Tạo mock service layer ghi nhận tham số cuộc gọi
	called := false
	mockSvc := &MockBackpressureServiceForTransport{
		ReportBackpressureFunc: func(ctx context.Context, zoneID string, queueLen int64, pendingLen int64, congested bool, epoch int64, congestionRate float64) error {
			if zoneID == "zone-xyz" && queueLen == 500 && pendingLen == 50 && congested && epoch == 12345 && congestionRate == 0.75 {
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

	// 3. Khởi chạy gRPC server và đăng ký BackpressureGRPCHandler
	grpcServer := grpc.NewServer()
	handler := coreRpcHandler.NewBackpressureGRPCHandler(mockSvc)
	coreProto.RegisterBackpressureServiceServer(grpcServer, handler)

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

	client := coreProto.NewBackpressureServiceClient(conn)

	// 5. Thực hiện cuộc gọi RPC ReportBackpressure thật qua mạng socket!
	req := &coreProto.ReportBackpressureRequest{
		ZoneId:         "zone-xyz",
		QueueLen:       500,
		PendingLen:     50,
		Congested:      true,
		Epoch:          12345,
		CongestionRate: 0.75,
	}

	res, err := client.ReportBackpressure(ctx, req)
	if err != nil {
		t.Fatalf("gRPC ReportBackpressure invocation failed: %v", err)
	}

	// 6. Kiểm chứng kết quả phản hồi từ gRPC server
	if !res.Success {
		t.Error("Expected report backpressure response success = true, got false")
	}

	if !called {
		t.Error("Service layer ReportBackpressure was not invoked with correct parameters")
	}
}
