package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	constant "controlplane/pkg/constant"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// SessionRefreshService chịu trách nhiệm thực thi toàn bộ logic làm mới/gia hạn phiên
// của cả End-User (Opaque Refresh & Trinity Sliding) và Admin (Sliding qua CAS).
type SessionRefreshService struct {
	repo        iamRepoInterface.RefreshTokenRepository
	adminRepo   iamRepoInterface.AdminAPIKeyRepository
	rbacRepo    iamRepoInterface.RbacRepository
	cacheEngine *cacheengine.CacheRegistry
	cfg         *config.Config
}

// NewSessionRefreshService khởi tạo một instance mới của SessionRefreshService.
func NewSessionRefreshService(
	cfg *config.Config,
	repo iamRepoInterface.RefreshTokenRepository,
	adminRepo iamRepoInterface.AdminAPIKeyRepository,
	rbacRepo iamRepoInterface.RbacRepository,
	cacheEngine *cacheengine.CacheRegistry,
) iamSvcInterface.SessionRefreshService {
	return &SessionRefreshService{
		repo:        repo,
		adminRepo:   adminRepo,
		rbacRepo:    rbacRepo,
		cacheEngine: cacheEngine,
		cfg:         cfg,
	}
}

// ======================================================================================================
// 1. OPAQUE REFRESH TOKEN (KIỂU 2 - END-USER) - DEPRECATED / REMOVED
// ======================================================================================================
// [COMMENT]: Luồng HTTP refresh xoay vòng token kiểu cũ đã bị xóa. Việc xác thực & khôi phục phiên
// hoàn toàn được thực hiện thông qua gRPC VerifyOpaqueRefreshToken kết nối từ ACL Gateway.


// ======================================================================================================
// 3. ADMIN TRINITY REFRESH (SRE SLIDING SESSION WITH CAS & 10S GRACE PERIOD)
// ======================================================================================================
func (s *SessionRefreshService) RefreshAdminTrinity(ctx context.Context, zoneCode string, ip *string, userAgent *string) (iamEntity.AdminLoginResult, error) {
	refreshOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		iamMetrics.ServiceCall(ctx, refreshOutcome)
	}()

	// [COMMENT]: Trích xuất accessKey và zoneID trực tiếp từ Go standard context
	var accessKey string
	var zoneID string
	if ident, ok := ctx.Value(constant.IdentityKey).(*constant.Identity); ok && ident != nil {
		accessKey = ident.AccessKey
		zoneID = ident.ZoneID
	}
	if strings.TrimSpace(accessKey) == "" {
		refreshOutcome = iamMetrics.OutcomeFailure
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, refreshOutcome)
	}
	if zoneID == "" {
		zoneID = "global"
	}

	// [COMMENT]: Phân giải mã phân vùng thành UUID qua L1 Cache Registry
	resolvedZoneID := "global"
	if !strings.EqualFold(zoneCode, "global") {
		val, err := s.cacheEngine.GetOrLoad(ctx, "zone_by_code", zoneCode)
		if err != nil {
			refreshOutcome = iamMetrics.OutcomeFailureUnknown
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
		}
		zoneIDStr, ok := val.(string)
		if !ok || zoneIDStr == "" {
			refreshOutcome = iamMetrics.OutcomeFailureUnknown
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrZoneUnavailable, fmt.Errorf("invalid zone ID resolved from code: %s", zoneCode), iamMetrics.OutcomeFailureUnknown)
		}
		resolvedZoneID = zoneIDStr
	}

	// [COMMENT]: Truy vấn và kiểm tra phiên làm việc hiện tại của Admin trong Redis L2
	now := time.Now()
	payload, _, exists, err := s.cacheEngine.L2.Get(ctx, "admin_access_session:"+accessKey+":"+zoneID)
	if err != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "GetAccessSession", iamMetrics.OutcomeFailureUnknown, time.Since(now), err)
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}
	if !exists {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "GetAccessSession", iamMetrics.OutcomeInvalidCredential, time.Since(now), err)
		refreshOutcome = iamMetrics.OutcomePreConditionFailed
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamMetrics.OutcomeInvalidCredential)
	}

	runtimeRecord := &iamproto.AdminAccessSession{}
	if err := proto.Unmarshal(payload, runtimeRecord); err != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "GetAccessSession", iamMetrics.OutcomeFailureUnknown, time.Since(now), err)
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}

	// [COMMENT]: Thực thi so sánh & gia hạn phiên bằng LUA Script để đảm bảo tính nguyên tử (CAS).
	// Session cũ sẽ được giữ lại 10 giây (grace period) để tránh làm gián đoạn các API request đồng thời.
	ipValue := ""
	if ip != nil {
		ipValue = strings.TrimSpace(*ip)
	}
	uaValue := ""
	if userAgent != nil {
		uaValue = strings.TrimSpace(*userAgent)
	}

	dataKey := "{admin_access_session:" + accessKey + ":" + zoneID + "}:data"
	versionKey := "{admin_access_session:" + accessKey + ":" + zoneID + "}:version"

	casLua := `
local current_ver = redis.call('GET', KEYS[2])
if not current_ver then
  return 0
end
if tonumber(current_ver) ~= tonumber(ARGV[1]) then
  return 0
end

local raw_data = redis.call('GET', KEYS[1])
if not raw_data then
  return 0
end

local next_ver = tonumber(current_ver) + 1

-- [COMMENT]: Phiên chạy Protobuf nhị phân chỉ cần cập nhật lại TTL (EXPIRE) cho data key trong grace period
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[2]))
redis.call('SET', KEYS[2], tostring(next_ver), 'EX', tonumber(ARGV[2]))
return 1
`

	resVal, casErr := s.cacheEngine.Exec.Execute(ctx, casLua, []string{dataKey, versionKey},
		runtimeRecord.Version, 10, time.Now().UTC().Unix(), ipValue, uaValue)
	if casErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, casErr, iamMetrics.OutcomeFailureUnknown)
	}
	resInt, _ := resVal.(int64)
	if resInt != 1 {
		refreshOutcome = iamMetrics.OutcomeFailure
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamMetrics.OutcomeFailure)
	}

	now = time.Now().UTC()

	// [COMMENT]: Sinh mới bộ ba trinity credentials (Access Key, Secret, JTI) cho Admin
	accessKeyNewUUID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, uuidErr, iamMetrics.OutcomeFailureUnknown)
	}
	accessKeyNew := accessKeyNewUUID.String()

	accessSecretNew, genErr := security.GenerateToken(48)
	if genErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, genErr, iamMetrics.OutcomeFailureUnknown)
	}

	tokenJTINewUUID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, uuidErr, iamMetrics.OutcomeFailureUnknown)
	}

	// Cập nhật IP/UA mới nếu thay đổi và đánh dấu dirty
	lastSeenIP := runtimeRecord.LastSeenIp
	lastSeenUA := runtimeRecord.LastSeenUserAgent
	lastSeenDirty := runtimeRecord.LastSeenDirty

	if ipValue != "" && lastSeenIP != ipValue {
		lastSeenIP = ipValue
		lastSeenDirty = true
	}
	if uaValue != "" && lastSeenUA != uaValue {
		lastSeenUA = uaValue
		lastSeenDirty = true
	}

	pbAdmin := &iamproto.AdminAccessSession{
		AccessKey:         accessKeyNew,
		AccessSecretHash:  security.HashTokenSHA256(accessSecretNew),
		TrackedDeviceId:   runtimeRecord.TrackedDeviceId,
		DevicePublicKey:   runtimeRecord.DevicePublicKey,
		TokenJti:          tokenJTINewUUID.String(),
		Version:           1,
		LastSeenAt:        now.Unix(),
		LastSeenIp:        lastSeenIP,
		LastSeenUserAgent: lastSeenUA,
		LastSeenDirty:     lastSeenDirty,
	}
	sessionNew, marshalErr := proto.Marshal(pbAdmin)
	if marshalErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, marshalErr, iamMetrics.OutcomeFailureUnknown)
	}

	if err := s.cacheEngine.L2.Set(ctx, "admin_access_session:"+accessKeyNew+":"+resolvedZoneID, sessionNew, 1, s.cfg.Security.AdminSessionTTL); err != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}

	// [COMMENT]: Ký JWT Admin Token mới bằng Vault
	adminAPITokenNew, signErr := security.SignWithSecret(security.Claims{
		Subject:   "sre",
		AccessKey: accessKeyNew,
		TokenID:   tokenJTINewUUID.String(),
		TokenUse:  "admin_api",
		ZoneID:    resolvedZoneID,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.cfg.Security.AdminSessionTTL).Unix(),
	}, nil)
	if signErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, signErr, iamMetrics.OutcomeFailureUnknown)
	}

	trackedUUID, _ := uuid.Parse(runtimeRecord.TrackedDeviceId)

	return iamEntity.AdminLoginResult{
		AdminAPIToken:  adminAPITokenNew,
		AccessKey:      accessKeyNew,
		AccessSecret:   accessSecretNew,
		ClientDeviceID: trackedUUID,
		ExpiresAt:      now.Add(s.cfg.Security.AdminSessionTTL),
	}, nil
}

// CreateRefreshToken tạo mới một session refresh token đục (opaque) khi đăng nhập thành công trên thiết bị tin cậy.
// Phương thức này sinh khóa ngẫu nhiên cao (high entropy), băm SHA256 để lưu trữ an toàn trong PostgreSQL qua repo,
// và trả về token thô cùng với thời gian hết hạn để AuthService đưa vào cookie/response.
// Token sinh ra có định dạng <userID>_<entropy> (độ dài 69 ký tự) để đảm bảo tính duy nhất tuyệt đối theo user.
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

// ======================================================================================================
// 4. CÁC PHƯƠNG THỨC THU HỒI PHỤ TRỢ (AUXILIARY REVOCATION METHODS)
// ======================================================================================================
func (s *SessionRefreshService) RevokeRefreshTokensByDeviceIDAndUserID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) error {
	_, err := s.repo.RevokeRefreshTokensByDeviceIDAndUserID(ctx, userID, deviceID)
	return err
}

func (s *SessionRefreshService) RevokeRefreshTokensByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) error {
	_, err := s.repo.RevokeRefreshTokensByUserID(ctx, userID, exceptDeviceID)
	return err
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
