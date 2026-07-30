package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

// SessionRefreshService chịu trách nhiệm thực thi toàn bộ logic làm mới/gia hạn phiên
// của cả End-User (Opaque Refresh & Trinity Sliding) và Admin (Sliding qua CAS).
type SessionRefreshService struct {
	repo             iamRepoInterface.RefreshTokenRepository
	rbacPlatformRepo iamRepoInterface.RbacPlatformRepository // [COMMENT]: Repo platform RBAC để check user role
	rbacTenantRepo   iamRepoInterface.RbacTenantRepository   // [COMMENT]: Repo tenant RBAC để check tenant role
	cacheEngine      *cacheengine.CacheRegistry
	cfg              *config.Config
	metrics          observability.WorkflowRecorder
}

// NewSessionRefreshService khởi tạo một instance mới của SessionRefreshService.
func NewSessionRefreshService(
	cfg *config.Config,
	repo iamRepoInterface.RefreshTokenRepository,
	rbacPlatformRepo iamRepoInterface.RbacPlatformRepository,
	rbacTenantRepo iamRepoInterface.RbacTenantRepository,
	cacheEngine *cacheengine.CacheRegistry,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.SessionRefreshService {
	return &SessionRefreshService{
		repo:             repo,
		rbacPlatformRepo: rbacPlatformRepo,
		rbacTenantRepo:   rbacTenantRepo,
		cacheEngine:      cacheEngine,
		cfg:              cfg,
		metrics:          metrics,
	}
}

// [COMMENT]: CreateRefreshToken tạo mới một session refresh token opaque khi đăng nhập thành công.
func (s *SessionRefreshService) CreateRefreshToken(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID, tenantID *uuid.UUID) (string, time.Time, error) {
	// [COMMENT]: 1. Tạo chuỗi entropy ngẫu nhiên dài 128 ký tự
	rawRefresh, err := security.GenerateToken(128)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("session refresh: failed to generate refresh token entropy: %w", err)
	}

	// [COMMENT]: 3. Tạo UUID v7 cho ID
	refreshID, err := uuid.NewV7()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("session refresh: failed to generate refresh ID: %w", err)
	}

	now := time.Now().UTC()
	refreshExp := now.Add(s.cfg.Security.RefreshTokenTTL)

	// [COMMENT]: 4. Chuẩn bị struct thực thể refresh token, lưu con trỏ tenantID nhận từ tham số
	rt := iamEntity.RefreshToken{
		ID:        refreshID,
		UserID:    userID,
		DeviceID:  &deviceID,
		TokenHash: security.HashTokenSHA256(rawRefresh),
		TenantID:  tenantID,
		IssuedAt:  now,
		ExpiresAt: refreshExp,
	}

	// [COMMENT]: 5. Ghi trực tiếp xuống DB PostgreSQL
	if err := s.repo.CreateToken(ctx, rt); err != nil {
		return "", time.Time{}, fmt.Errorf("session refresh: failed to persist refresh session: %w", err)
	}

	return rawRefresh, refreshExp, nil
}

// [COMMENT]: VerifyOpaqueRefreshToken thực hiện kiểm tra tính hợp lệ của Refresh Token
func (s *SessionRefreshService) VerifyOpaqueRefreshToken(ctx context.Context, rawRefreshToken string, tenantID *uuid.UUID, userID uuid.UUID) (*iamEntity.VerifyOpaqueRefreshTokenResult, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: 1. Thực hiện băm SHA-256 token thô từ client để so khớp
	tokenHash := security.HashTokenSHA256(rawRefreshToken)
	refreshContext, err := s.repo.LoadContextByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
			return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
		}
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "internal")
	}

	session := &refreshContext.Session
	// [COMMENT]: 2. Kiểm tra xem session token đã quá hạn sử dụng hay chưa
	if time.Now().UTC().After(session.ExpiresAt) {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
	}

	user := &refreshContext.User
	// [COMMENT]: 3. Xác minh tính khớp cấu trúc: User ID truyền lên phải khớp với token được lưu
	if user.ID != userID {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
	}

	// [COMMENT]: Đối chiếu Tenant ID
	if tenantID == nil {
		if session.TenantID != nil {
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
			return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
		}
	} else {
		if session.TenantID == nil || *session.TenantID != *tenantID {
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
			return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
		}
	}

	// [COMMENT]: 4. Kiểm tra trạng thái tài khoản user (tránh các user bị khóa/treo/chưa kích hoạt)
	if user.Status == iamEntity.UserStatusPendingActive || user.Status == iamEntity.UserStatusSuspended || user.Status == iamEntity.UserStatusDisabled {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
	}

	// [COMMENT]: 5. Đảm bảo thiết bị của user không bị thu hồi quyền truy cập (revoked_at IS NULL)
	if refreshContext.Device == nil || refreshContext.Device.RevokedAt != nil {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
	}

	// [COMMENT]: 6. Xác định role và level của user/tenant trực tiếp từ database rbac tables thông qua repository riêng biệt
	var roleIDStr string
	var roleLevel int32
	if tenantID == nil {
		roleIDStr, roleLevel, err = s.rbacPlatformRepo.GetRoleIDByUserID(ctx, userID)
	} else {
		roleIDStr, roleLevel, err = s.rbacTenantRepo.GetRoleIDByTenantID(ctx, *tenantID)
	}
	if err != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "internal")
	}

	tenantIDStr := ""
	if session.TenantID != nil {
		tenantIDStr = session.TenantID.String()
	}

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return &iamEntity.VerifyOpaqueRefreshTokenResult{
		Valid:    true,
		UserID:   user.ID.String(),
		TenantID: tenantIDStr,
		RoleID:   roleIDStr,
		Level:    roleLevel,
		Username: user.Username,
	}, nil
}

// [COMMENT]: RevokeOpaqueRefreshToken thực hiện băm token thô nhận từ ACR gRPC và thực thi xóa khỏi database.
func (s *SessionRefreshService) RevokeOpaqueRefreshToken(ctx context.Context, rawRefreshToken string) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	if rawRefreshToken == "" {
		result, reason = observability.ResultSuccess, observability.ReasonNone
		return nil
	}
	tokenHash := security.HashTokenSHA256(rawRefreshToken)

	_, err := s.repo.DeleteByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrZeroRowsAffected) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
			return err
		}
		return fmt.Errorf("session refresh: failed to delete refresh token: %w", err)
	}

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}
