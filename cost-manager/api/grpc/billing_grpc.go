package grpc

import (
	"context"

	"cost-manager/api/internal/transport/proto/billingproto"
	"cost-manager/api/internal/domain/repo"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type BillingGrpcServer struct {
	billingproto.UnimplementedBillingServiceServer
	repo repo.WalletRepository
}

func NewBillingGrpcServer(repo repo.WalletRepository) *BillingGrpcServer {
	return &BillingGrpcServer{repo: repo}
}

// [COMMENT]: CheckWalletStatus thực hiện kiểm tra trạng thái ví qua gRPC phục vụ mTLS server-to-server check
func (s *BillingGrpcServer) CheckWalletStatus(ctx context.Context, req *billingproto.WalletStatusRequest) (*billingproto.WalletStatusResponse, error) {
	ownerID, err := uuid.Parse(req.OwnerId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid owner_id format: %v", err)
	}

	wallet, err := s.repo.GetOrCreateWallet(ctx, ownerID, req.OwnerType)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get wallet: %v", err)
	}

	return &billingproto.WalletStatusResponse{
		WalletId:       wallet.ID.String(),
		Status:         string(wallet.Status),
		Balance:        wallet.Balance,
		OverdraftLimit: wallet.OverdraftLimit,
	}, nil
}
