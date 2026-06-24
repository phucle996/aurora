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
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

// SessionRefreshService chịu trách nhiệm thực thi toàn bộ logic làm mới/gia hạn phiên
// của cả End-User (Opaque Refresh & Trinity Sliding) và Admin (Sliding qua CAS).
type SessionRefreshService struct {
	repo        iamRepoInterface.RefreshTokenRepository
	rbacRepo    iamRepoInterface.RbacRepository
	cacheEngine *cacheengine.CacheRegistry
	cfg         *config.Config
}

// NewSessionRefreshService khởi tạo một instance mới của SessionRefreshService.
func NewSessionRefreshService(
	cfg *config.Config,
	repo iamRepoInterface.RefreshTokenRepository,
	rbacRepo iamRepoInterface.RbacRepository,
	cacheEngine *cacheengine.CacheRegistry,
) iamSvcInterface.SessionRefreshService {
	return &SessionRefreshService{
		repo:        repo,
		rbacRepo:    rbacRepo,
		cacheEngine: cacheEngine,
		cfg:         cfg,
	}
}

func (s *SessionRefreshService) CreateRefreshToken(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (string, time.Time, error) {
	// [COMMENT]: 1. Tạo chuỗi entropy ngẫu nhiên dài 32 ký tự
	entropy, err := security.GenerateToken(32)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("session refresh: failed to generate refresh token entropy: %w", err)
	}

	// [COMMENT]: 2. Kết hợp với userID để tạo token hoàn chỉnh định dạng user_token (tổng cộng 36 + 1 + 32 = 69 ký tự)
	rawRefresh := fmt.Sprintf("%s_%s", userID.String(), entropy)

	// [COMMENT]: 3. Tạo UUID v7 cho ID (phục vụ mục đích truy vết tuyến tính và rotation)
	refreshID, err := uuid.NewV7()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("session refresh: failed to generate refresh ID: %w", err)
	}

	now := time.Now().UTC()
	refreshExp := now.Add(s.cfg.Security.RefreshTokenTTL)

	// [COMMENT]: 4. Chuẩn bị struct thực thể refresh token
	rt := iamEntity.RefreshToken{
		ID:        refreshID,
		UserID:    userID,
		DeviceID:  &deviceID,
		TokenHash: security.HashTokenSHA256(rawRefresh),
		TenantID:  nil,
		IssuedAt:  now,
		ExpiresAt: refreshExp,
	}

	// [COMMENT]: 5. Ghi trực tiếp xuống DB PostgreSQL thông qua Repository của RefreshToken
	if err := s.repo.CreateRefreshTokenSession(ctx, rt); err != nil {
		return "", time.Time{}, fmt.Errorf("session refresh: failed to persist refresh session: %w", err)
	}

	return rawRefresh, refreshExp, nil
}

// VerifyOpaqueRefreshToken thực hiện kiểm tra tính hợp lệ của Refresh Token
// acl call để cấp trinity token mới
func (s *SessionRefreshService) VerifyOpaqueRefreshToken(ctx context.Context, rawRefreshToken string, scope string) (*iamEntity.VerifyOpaqueRefreshTokenResult, error) {
	// [COMMENT]: 1. Thực hiện băm SHA-256 token thô từ client để so khớp với cơ sở dữ liệu
	tokenHash := security.HashTokenSHA256(rawRefreshToken)
	refreshContext, err := s.repo.LoadRefreshContextByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrNotFound) {
			// [COMMENT]: Không tìm thấy session refresh token tương ứng
			return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
		}
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}

	session := &refreshContext.Session
	// [COMMENT]: 2. Kiểm tra xem session token đã quá hạn sử dụng hay chưa
	if time.Now().UTC().After(session.ExpiresAt) {
		return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
	}

	user := &refreshContext.User
	// [COMMENT]: 3. Đảm bảo bản ghi user liên kết là hợp lệ
	if user.ID == (uuid.UUID{}) {
		return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
	}

	// [COMMENT]: 4. Kiểm tra trạng thái tài khoản user (tránh các user bị khóa/treo/chưa kích hoạt)
	if user.Status == iamEntity.UserStatusPendingActive || user.Status == iamEntity.UserStatusSuspended || user.Status == iamEntity.UserStatusDisabled {
		return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
	}

	// [COMMENT]: 5. Đảm bảo thiết bị của user không bị thu hồi quyền truy cập (revoked)
	if refreshContext.Device == nil || refreshContext.Device.Status == iamEntity.DeviceStatusRevoked {
		return &iamEntity.VerifyOpaqueRefreshTokenResult{Valid: false}, nil
	}

	// [COMMENT]: 6. Xác định role và level của user dựa trên scope truyền từ Gateway
	roleCode, roleLevel, err := s.rbacRepo.GetUserRoleAndLevelByScope(ctx, user.ID, scope)
	if err != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}

	// [COMMENT]: 7. Lấy tenant_id từ session (nếu có)
	tenantIDStr := ""
	if session.TenantID != nil {
		tenantIDStr = session.TenantID.String()
	}

	return &iamEntity.VerifyOpaqueRefreshTokenResult{
		Valid:    true,
		UserID:   user.ID.String(),
		TenantID: tenantIDStr,
		Role:     roleCode,
		Level:    int32(roleLevel),
	}, nil
}

// [COMMENT]: RevokeOpaqueRefreshToken thực hiện băm token thô nhận từ ACL gRPC và thực thi xóa khỏi database.
// Trả về ErrZeroRowsAffected nếu không tìm thấy bản ghi để tầng vận chuyển tự quyết định log/phản hồi.
func (s *SessionRefreshService) RevokeOpaqueRefreshToken(ctx context.Context, rawRefreshToken string) error {
	if rawRefreshToken == "" {
		return nil
	}
	// [COMMENT]: Băm SHA-256 mã token thô để so khớp bảo mật
	tokenHash := security.HashTokenSHA256(rawRefreshToken)

	startLoad := time.Now()
	_, err := s.repo.DeleteRefreshTokenSessionByHash(ctx, tokenHash)
	if err != nil {
		// [COMMENT]: Nếu là lỗi ErrZeroRowsAffected, cập nhật metric thành công và trả lỗi lên lớp trên
		if errors.Is(err, iamTaxonomy.ErrZeroRowsAffected) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "DeleteRefreshTokenSessionByHash", iamMetrics.OutcomeSuccess, time.Since(startLoad), nil)
			return err
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "DeleteRefreshTokenSessionByHash", iamMetrics.OutcomeFailureUnknown, time.Since(startLoad), err)
		return fmt.Errorf("session refresh: failed to delete refresh token: %w", err)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "DeleteRefreshTokenSessionByHash", iamMetrics.OutcomeSuccess, time.Since(startLoad), nil)

	return nil
}
