package rpcHandler

import (
	"context"
	"controlplane/internal/iam/domain/service"
	"controlplane/internal/iam/transport/rpc/proto"
)

// AuthGRPCHandler là thin gRPC transport handler, nhận request và ủy quyền xử lý cho AuthService
type AuthGRPCHandler struct {
	iamproto.UnimplementedAuthServiceServer
	authService iamSvcInterface.AuthService // Tầng nghiệp vụ xử lý chính
}

// NewAuthGRPCHandler khởi tạo mới một AuthGRPCHandler
func NewAuthGRPCHandler(authService iamSvcInterface.AuthService) *AuthGRPCHandler {
	// [ignoring loop detection]
	return &AuthGRPCHandler{authService: authService}
}

// VerifyAdminTrinityToken tiếp nhận và xử lý yêu cầu xác thực Admin qua gRPC
func (h *AuthGRPCHandler) VerifyAdminTrinityToken(ctx context.Context, req *iamproto.VerifyAdminTrinityTokenRequest) (*iamproto.VerifyAdminTrinityTokenResponse, error) {
	// 1. Kiểm tra nhanh các trường dữ liệu rỗng đầu vào
	if req.AdminApiToken == "" || req.AccessKey == "" || req.AccessSecret == "" {
		return &iamproto.VerifyAdminTrinityTokenResponse{Valid: false}, nil
	}

	// 2. Gọi hàm nghiệp vụ tại AuthService để xác thực phiên
	res, err := h.authService.VerifyAdminTrinitySession(ctx, req.AdminApiToken, req.AccessKey, req.AccessSecret)
	if err != nil {
		return &iamproto.VerifyAdminTrinityTokenResponse{Valid: false}, nil
	}

	return &iamproto.VerifyAdminTrinityTokenResponse{
		Valid:  res.Valid,
		UserId: res.UserID,
		Role:   res.Role,
	}, nil
}

// VerifyUserTrinityToken tiếp nhận và xử lý yêu cầu xác thực User qua gRPC
func (h *AuthGRPCHandler) VerifyUserTrinityToken(ctx context.Context, req *iamproto.VerifyUserTrinityTokenRequest) (*iamproto.VerifyUserTrinityTokenResponse, error) {
	// 1. Kiểm tra nhanh các trường dữ liệu rỗng đầu vào
	if req.AccessToken == "" || req.AccessKey == "" || req.AccessSecret == "" {
		return &iamproto.VerifyUserTrinityTokenResponse{Valid: false}, nil
	}

	// 2. Gọi hàm nghiệp vụ tại AuthService để xác thực phiên
	res, err := h.authService.VerifyUserTrinitySession(ctx, req.AccessToken, req.AccessKey, req.AccessSecret)
	if err != nil {
		return &iamproto.VerifyUserTrinityTokenResponse{Valid: false}, nil
	}

	return &iamproto.VerifyUserTrinityTokenResponse{
		Valid:  res.Valid,
		UserId: res.UserID,
		Role:   res.Role,
		ZoneId: res.ZoneID,
	}, nil
}
