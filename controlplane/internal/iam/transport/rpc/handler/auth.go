package rpcHandler

import (
	"context"
	"controlplane/internal/iam/domain/service"
	"controlplane/internal/iam/transport/rpc/proto"
)

// AuthGRPCHandler là thin gRPC transport handler, nhận request và ủy quyền xử lý cho các service tương ứng.
type AuthGRPCHandler struct {
	iamproto.UnimplementedAuthServiceServer
	authService        iamSvcInterface.AuthService        // Tầng nghiệp vụ xử lý chính cho User
	adminAPIKeyService iamSvcInterface.AdminAPIKeyService // Tầng nghiệp vụ xử lý chính cho Admin
}

// NewAuthGRPCHandler khởi tạo mới một AuthGRPCHandler
func NewAuthGRPCHandler(authService iamSvcInterface.AuthService, adminAPIKeyService iamSvcInterface.AdminAPIKeyService) *AuthGRPCHandler {
	// [ignoring loop detection]
	return &AuthGRPCHandler{
		authService:        authService,
		adminAPIKeyService: adminAPIKeyService,
	}
}

// VerifyAdminTrinityToken tiếp nhận và xử lý yêu cầu xác thực Admin qua gRPC
func (h *AuthGRPCHandler) VerifyAdminTrinityToken(ctx context.Context, req *iamproto.VerifyAdminTrinityTokenRequest) (*iamproto.VerifyAdminTrinityTokenResponse, error) {
	// 1. Kiểm tra nhanh các trường dữ liệu rỗng đầu vào
	if req.AdminApiToken == "" || req.AccessKey == "" || req.AccessSecret == "" {
		return &iamproto.VerifyAdminTrinityTokenResponse{Valid: false}, nil
	}

	// 2. Gọi hàm nghiệp vụ tại AdminAPIKeyService để xác thực phiên
	res, err := h.adminAPIKeyService.VerifyAdminTrinitySession(ctx, req.AdminApiToken, req.AccessKey, req.AccessSecret)
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

// VerifyOpaqueRefreshToken tiếp nhận và xử lý yêu cầu xác thực Opaque Refresh Token từ Gateway qua gRPC
func (h *AuthGRPCHandler) VerifyOpaqueRefreshToken(ctx context.Context, req *iamproto.VerifyOpaqueRefreshTokenRequest) (*iamproto.VerifyOpaqueRefreshTokenResponse, error) {
	// [COMMENT]: 1. Kiểm tra nhanh trường dữ liệu rỗng đầu vào
	if req.RefreshToken == "" {
		return &iamproto.VerifyOpaqueRefreshTokenResponse{
			Valid:        false,
			ErrorMessage: "empty refresh token",
		}, nil
	}

	// [COMMENT]: 2. Gọi hàm nghiệp vụ tại AuthService để xác thực token và phân giải scope
	res, err := h.authService.VerifyOpaqueRefreshToken(ctx, req.RefreshToken, req.Scope)
	if err != nil {
		return &iamproto.VerifyOpaqueRefreshTokenResponse{
			Valid:        false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &iamproto.VerifyOpaqueRefreshTokenResponse{
		Valid:    res.Valid,
		UserId:   res.UserID,
		TenantId: res.TenantID,
		Role:     res.Role,
		Level:    res.Level,
		ZoneId:   res.ZoneID,
	}, nil
}
