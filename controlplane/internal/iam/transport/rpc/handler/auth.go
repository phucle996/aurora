package rpcHandler

import (
	"context"
	"errors"

	"github.com/google/uuid"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"
)

// AuthGRPCHandler là thin gRPC transport handler, nhận request và ủy quyền xử lý cho các service tương ứng.
type AuthGRPCHandler struct {
	iamproto.UnimplementedAuthServiceServer
	authService           iamSvcInterface.AuthService           // Tầng nghiệp vụ xử lý chính cho User
	sessionRefreshService iamSvcInterface.SessionRefreshService // Tầng nghiệp vụ xử lý Session Refresh
}

// NewAuthGRPCHandler khởi tạo mới một AuthGRPCHandler
func NewAuthGRPCHandler(
	authService iamSvcInterface.AuthService,
	sessionRefreshService iamSvcInterface.SessionRefreshService,
) *AuthGRPCHandler {
	// [ignoring loop detection]
	return &AuthGRPCHandler{
		authService:           authService,
		sessionRefreshService: sessionRefreshService,
	}
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

	// [COMMENT]: 2. Gọi hàm nghiệp vụ tại SessionRefreshService để xác thực token và phân giải scope
	res, err := h.sessionRefreshService.VerifyOpaqueRefreshToken(ctx, req.RefreshToken, req.Scope)
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
		Username: res.Username,
	}, nil
}

// [COMMENT]: RevokeOpaqueRefreshToken tiếp nhận và xử lý yêu cầu thu hồi refresh token từ ACR gửi qua gRPC
func (h *AuthGRPCHandler) RevokeOpaqueRefreshToken(ctx context.Context, req *iamproto.RevokeOpaqueRefreshTokenRequest) (*iamproto.RevokeOpaqueRefreshTokenResponse, error) {
	// [COMMENT]: Gọi trực tiếp hàm nghiệp vụ tại SessionRefreshService để thu hồi token
	err := h.sessionRefreshService.RevokeOpaqueRefreshToken(ctx, req.RefreshToken)
	if err != nil {
		// [COMMENT]: Nếu không tìm thấy token trong DB, ghi nhận log cảnh báo
		if errors.Is(err, iamTaxonomy.ErrZeroRowsAffected) {
			logger.SysWarn("RevokeOpaqueRefreshToken", "Refresh token record not found for hash")
		} else {
			// [COMMENT]: Ghi log lỗi hệ thống chi tiết nếu gặp lỗi cơ sở dữ liệu thực sự
			logger.SysErrorFields("RevokeOpaqueRefreshToken", "Failed to revoke refresh token from database", err, nil)
		}
		// [COMMENT]: Luôn trả về thành công (nil error) cho client vì việc logout thực tế của user đã hoàn tất tại Gateway
		return &iamproto.RevokeOpaqueRefreshTokenResponse{}, nil
	}

	logger.SysInfo("RevokeOpaqueRefreshToken", "Successfully deleted refresh token session")
	return &iamproto.RevokeOpaqueRefreshTokenResponse{}, nil
}

// [COMMENT]: VerifyUserCredentials tiếp nhận và xử lý yêu cầu xác thực credentials & thiết bị qua gRPC từ Gateway
func (h *AuthGRPCHandler) VerifyUserCredentials(ctx context.Context, req *iamproto.VerifyUserCredentialsRequest) (*iamproto.VerifyUserCredentialsResponse, error) {
	// [COMMENT]: Kiểm tra các tham số đầu vào cơ bản
	const op = "VerifyUserCredentials"
	if req.Username == "" || req.Password == "" {
		logger.SysWarn(op, "Username and password are required")
		return &iamproto.VerifyUserCredentialsResponse{
			Valid:        false,
			ErrorMessage: "Username and password are required",
		}, nil
	}

	var clientDeviceID uuid.UUID
	if req.ClientDeviceId != "" {
		parsed, err := uuid.Parse(req.ClientDeviceId)
		if err == nil {
			clientDeviceID = parsed
		}
	}

	// [COMMENT]: Map sang LoginRequest của Domain Entity
	loginReq := iamEntity.LoginRequest{
		Username:        req.Username,
		Password:        req.Password,
		DevicePublicKey: req.PublicKey,
		TrustDevice:     req.TrustDevice,
		DeviceName:      req.DeviceName,
		ClientDeviceID:  clientDeviceID,
	}

	// [COMMENT]: Gắn Remote IP và User Agent vào Context phục vụ cho việc ghi nhận lịch sử thiết bị
	ctx = context.WithValue(ctx, constant.RemoteIPKey, req.ClientIp)
	ctx = context.WithValue(ctx, constant.UserAgentKey, req.UserAgent)

	// [COMMENT]: Gọi AuthService để xác thực credentials và tạo mới session (nếu trust_device=true)
	res, err := h.authService.VerifyUserCredentials(ctx, loginReq)
	if err != nil {
		logger.SysErrorFields("VerifyUserCredentials", "Failed to verify credentials", err, nil)
		return &iamproto.VerifyUserCredentialsResponse{
			Valid:        false,
			ErrorMessage: err.Error(),
		}, nil
	}

	return &iamproto.VerifyUserCredentialsResponse{
		Valid:          res.Valid,
		UserId:         res.UserID,
		Role:           res.Role,
		Level:          res.Level,
		TenantId:       res.TenantID,
		ClientDeviceId: res.ClientDeviceID,
		RefreshToken:   res.RefreshToken,
		Username:       res.Username,
	}, nil
}
