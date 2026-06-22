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
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	constant "controlplane/pkg/constant"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
// 1. OPAQUE REFRESH TOKEN (KIỂU 2 - END-USER)
// ======================================================================================================
func (s *SessionRefreshService) RefreshUserOpaque(ctx context.Context, rawRefreshToken string) (*iamEntity.RefreshTokenResult, error) {
	const workflow = "refresh_user_opaque"

	refreshOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		iamMetrics.ServiceCall(ctx, refreshOutcome)
	}()

	// [COMMENT]: Thực hiện băm SHA256 Refresh Token thô nhận được từ phía Client để so khớp với DB.
	startLoad := time.Now()
	refreshContext, ctxErr := s.repo.LoadRefreshContextByHash(ctx, security.HashTokenSHA256(rawRefreshToken))
	if ctxErr != nil {
		if errors.Is(ctxErr, iamTaxonomy.ErrNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "LoadRefreshContextByHash", iamMetrics.OutcomeInvalidCredential, time.Since(startLoad), ctxErr)
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, ctxErr, iamMetrics.OutcomeInvalidCredential)
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "LoadRefreshContextByHash", iamMetrics.OutcomeFailureUnknown, time.Since(startLoad), ctxErr)
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, ctxErr, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "LoadRefreshContextByHash", iamMetrics.OutcomeSuccess, time.Since(startLoad), nil)

	session := &refreshContext.Session
	// [COMMENT]: Kiểm tra xem token đã quá hạn sử dụng hay chưa
	if time.Now().UTC().After(session.ExpiresAt) {
		refreshOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamMetrics.OutcomeInvalidCredential)
	}
	trackedDeviceID := *session.DeviceID

	user := &refreshContext.User
	// [COMMENT]: Xác nhận tài khoản người dùng có tồn tại hợp lệ
	if user.ID == (uuid.UUID{}) {
		refreshOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamMetrics.OutcomeInvalidCredential)
	}
	// [COMMENT]: Kiểm tra trạng thái tài khoản để ngăn chặn các phiên của user bị treo/vô hiệu hóa/đang chờ kích hoạt
	if user.Status == iamEntity.UserStatusPendingActive || user.Status == iamEntity.UserStatusSuspended || user.Status == iamEntity.UserStatusDisabled {
		refreshOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamMetrics.OutcomeInvalidCredential)
	}
	// [COMMENT]: Kiểm tra trạng thái thiết bị có bị thu hồi quyền truy cập (Revoked) hay không
	if refreshContext.Device == nil || refreshContext.Device.Status == iamEntity.DeviceStatusRevoked {
		refreshOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamMetrics.OutcomeInvalidCredential)
	}

	now := time.Now().UTC()

	// [COMMENT]: Tạo mới Token ID sử dụng UUIDv7 đóng vai trò JTI Claim
	accessJTI, idErr := uuid.NewV7()
	if idErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, idErr, iamMetrics.OutcomeFailureUnknown)
	}
	// [COMMENT]: Sinh ngẫu nhiên access key để làm định danh phiên làm việc mới
	accessKey := uuid.NewString()
	// [COMMENT]: Sinh ngẫu nhiên access secret dài 32 bytes có tính mật mã bảo mật cao
	accessSecret, secretErr := security.GenerateToken(32)
	if secretErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, secretErr, iamMetrics.OutcomeFailureUnknown)
	}
	accessExpiresAt := now.Add(s.cfg.Security.AccessSecretTTL)

	// [COMMENT]: Ký JWT access token mới trực tiếp bằng Vault
	accessToken, accessErr := security.SignWithSecret(security.Claims{
		Subject:   user.ID.String(),
		Role:      "",
		Level:     0,
		AccessKey: accessKey,
		TokenID:   accessJTI.String(),
		TokenUse:  "access",
		IssuedAt:  now.Unix(),
		ExpiresAt: accessExpiresAt.Unix(),
	}, nil)
	if accessErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, accessErr, iamMetrics.OutcomeFailureUnknown)
	}

	// [COMMENT]: Tạo refresh token tiếp theo (rotation token)
	rawNextRefreshToken, refreshErr := security.GenerateToken(43)
	if refreshErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, refreshErr, iamMetrics.OutcomeFailureUnknown)
	}
	nextRefreshID, refreshIDErr := uuid.NewV7()
	if refreshIDErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrUuidGenerateFailed, refreshIDErr, iamMetrics.OutcomeFailureUnknown)
	}
	nextRefreshExpiresAt := now.Add(s.cfg.Security.RefreshTokenTTL)
	nextRefreshToken := iamEntity.RefreshToken{
		ID:            nextRefreshID,
		UserID:        session.UserID,
		DeviceID:      &trackedDeviceID,
		TokenHash:     security.HashTokenSHA256(rawNextRefreshToken),
		TokenFamilyID: session.TokenFamilyID,
		TenantID:      nil,
		IssuedAt:      now,
		ExpiresAt:     nextRefreshExpiresAt,
	}

	// [COMMENT]: Thực thi xoay vòng refresh token đồng bộ ở Postgres DB trong 1 Transaction
	startRotate := time.Now()
	if rotateErr := s.repo.RotateRefreshToken(ctx, *session, nextRefreshToken); rotateErr != nil {
		if errors.Is(rotateErr, iamTaxonomy.ErrInvalidSession) || errors.Is(rotateErr, pgx.ErrNoRows) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RotateRefreshToken", iamMetrics.OutcomeInvalidCredential, time.Since(startRotate), rotateErr)
			refreshOutcome = iamMetrics.OutcomeInvalidCredential
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, rotateErr, iamMetrics.OutcomeInvalidCredential)
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RotateRefreshToken", iamMetrics.OutcomeFailureUnknown, time.Since(startRotate), rotateErr)
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, rotateErr, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RotateRefreshToken", iamMetrics.OutcomeSuccess, time.Since(startRotate), nil)

	// [COMMENT]: Đồng bộ trạng thái runtime của thiết bị vào Redis cache engine L2
	newAccessSecretHash := security.HashTokenSHA256(accessSecret)
	pbUser := &iamproto.UserAccessSession{
		Ash:  newAccessSecretHash,
		Tdid: trackedDeviceID.String(),
		Lsa:  now.Unix(),
	}
	payload, marshalErr := proto.Marshal(pbUser)
	if marshalErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, marshalErr, iamMetrics.OutcomeFailureUnknown)
	}

	sessionKey := "iam:user_access_session:" + user.ID.String() + ":" + accessKey
	indexKey := "iam:user_access_index:" + user.ID.String()
	runtimeTTL := s.cfg.Security.AccessSecretTTL

	// [COMMENT]: Kiểm tra an toàn xem cacheEngine hoặc L2 client có bị nil không (phổ biến trong môi trường chạy test cô lập)
	// để tránh panic runtime và trả về lỗi ErrAuthenticationUnavailable đúng nghiệp vụ.
	if s.cacheEngine == nil || s.cacheEngine.L2 == nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, fmt.Errorf("cache engine L2 client is nil"), iamMetrics.OutcomeFailureUnknown)
	}

	startSetRuntime := time.Now()
	rdb := s.cacheEngine.L2.Client()
	pipe := rdb.Pipeline()
	pipe.Set(ctx, sessionKey, payload, runtimeTTL)
	pipe.SAdd(ctx, indexKey, accessKey)
	pipe.Expire(ctx, indexKey, runtimeTTL*3)
	_, setErr := pipe.Exec(ctx)

	if setErr != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL1, "setTrinityToken", iamMetrics.OutcomeFailureUnknown, time.Since(startSetRuntime), setErr)
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, setErr, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL1, "setTrinityToken", iamMetrics.OutcomeSuccess, time.Since(startSetRuntime), nil)

	return &iamEntity.RefreshTokenResult{
		AccessToken:      accessToken,
		RefreshToken:     rawNextRefreshToken,
		AccessKey:        accessKey,
		AccessSecret:     accessSecret,
		TrackedDeviceID:  trackedDeviceID.String(),
		AccessExpiresAt:  accessExpiresAt,
		RefreshExpiresAt: nextRefreshExpiresAt,
	}, nil
}

// ======================================================================================================
// 2. TRINITY REFRESH (KIỂU 1 - END-USER SLIDING SESSION)
// ======================================================================================================
func (s *SessionRefreshService) RefreshUserTrinity(ctx context.Context, userID uuid.UUID, oldAccessKey, oldAccessSecret string) (*iamEntity.TrinityRefreshResult, error) {
	trinityOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		iamMetrics.ServiceCall(ctx, trinityOutcome)
	}()

	oldAccessKey = strings.TrimSpace(oldAccessKey)
	oldAccessSecret = strings.TrimSpace(oldAccessSecret)
	if oldAccessKey == "" || oldAccessSecret == "" {
		trinityOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}

	// [COMMENT]: Đảm bảo an toàn cacheEngine không nil khi thực hiện Trinity Refresh trong môi trường test cô lập.
	if s.cacheEngine == nil || s.cacheEngine.L2 == nil {
		trinityOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, fmt.Errorf("cache engine L2 client is nil"), iamMetrics.OutcomeFailureUnknown)
	}

	rdb := s.cacheEngine.L2.Client()
	userIDStr := userID.String()
	oldSessionKey := "iam:user_access_session:" + userIDStr + ":" + oldAccessKey
	indexKey := "iam:user_access_index:" + userIDStr

	// [COMMENT]: Đọc session cũ từ Redis. Nếu key không tồn tại (TTL đã hết) → session đã chết
	rawPayload, getErr := rdb.Get(ctx, oldSessionKey).Result()
	if getErr != nil {
		trinityOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, getErr, iamMetrics.OutcomeInvalidCredential)
	}

	var pbUser iamproto.UserAccessSession
	if unmarshalErr := proto.Unmarshal([]byte(rawPayload), &pbUser); unmarshalErr != nil {
		trinityOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to unmarshal old session: %v", iamTaxonomy.ErrAuthenticationUnavailable, unmarshalErr)
	}
	oldRecord := iamEntity.UserAccessSession{
		AccessSecretHash: pbUser.Ash,
		TrackedDeviceID:  pbUser.Tdid,
		LastSeenAt:       pbUser.Lsa,
	}

	// [COMMENT]: Xác minh access_secret cũ khớp với hash lưu trong Redis
	if oldRecord.AccessSecretHash != security.HashTokenSHA256(oldAccessSecret) {
		trinityOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}

	now := time.Now().UTC()

	// [COMMENT]: Sinh bộ trinity mới gồm access_key, access_secret và JWT access_token
	newAccessKeyID, keyErr := uuid.NewV7()
	if keyErr != nil {
		trinityOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to generate new access key: %v", iamTaxonomy.ErrAuthenticationUnavailable, keyErr)
	}
	newAccessKey := newAccessKeyID.String()
	newAccessSecret, secretErr := security.GenerateToken(32)
	if secretErr != nil {
		trinityOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to generate new access secret: %v", iamTaxonomy.ErrAuthenticationUnavailable, secretErr)
	}
	newJTI, jtiErr := uuid.NewV7()
	if jtiErr != nil {
		trinityOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to generate new JTI: %v", iamTaxonomy.ErrAuthenticationUnavailable, jtiErr)
	}
	newAccessExp := now.Add(s.cfg.Security.AccessSecretTTL)

	// [COMMENT]: Ký JWT access token mới bằng Vault
	newAccessToken, signErr := security.SignWithSecret(security.Claims{
		Subject:   userIDStr,
		Role:      "",
		Level:     0,
		AccessKey: newAccessKey,
		TokenID:   newJTI.String(),
		TokenUse:  "access",
		IssuedAt:  now.Unix(),
		ExpiresAt: newAccessExp.Unix(),
	}, nil)
	if signErr != nil {
		trinityOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to sign new access token: %v", iamTaxonomy.ErrAuthenticationUnavailable, signErr)
	}

	// [COMMENT]: Ghi session mới + xoá session cũ trong cùng 1 Redis transaction pipeline
	newPbUser := &iamproto.UserAccessSession{
		Ash:  security.HashTokenSHA256(newAccessSecret),
		Tdid: oldRecord.TrackedDeviceID,
		Lsa:  now.Unix(),
	}
	newPayload, marshalErr := proto.Marshal(newPbUser)
	if marshalErr != nil {
		trinityOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to marshal new session: %v", iamTaxonomy.ErrAuthenticationUnavailable, marshalErr)
	}

	newSessionKey := "iam:user_access_session:" + userIDStr + ":" + newAccessKey
	pipe := rdb.TxPipeline()
	pipe.Set(ctx, newSessionKey, newPayload, s.cfg.Security.AccessSecretTTL)
	pipe.SAdd(ctx, indexKey, newAccessKey)
	pipe.Del(ctx, oldSessionKey)
	pipe.SRem(ctx, indexKey, oldAccessKey)
	pipe.Expire(ctx, indexKey, s.cfg.Security.AccessSecretTTL+24*time.Hour)
	if _, pipeErr := pipe.Exec(ctx); pipeErr != nil {
		trinityOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to swap trinity in redis: %v", iamTaxonomy.ErrAuthenticationUnavailable, pipeErr)
	}

	return &iamEntity.TrinityRefreshResult{
		AccessToken:     newAccessToken,
		AccessKey:       newAccessKey,
		AccessSecret:    newAccessSecret,
		TrackedDeviceID: oldRecord.TrackedDeviceID,
		AccessExpiresAt: newAccessExp,
	}, nil
}

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

	// [COMMENT]: 3. Tạo UUID v7 cho ID và Family ID (phục vụ mục đích truy vết tuyến tính và rotation)
	refreshID, err := uuid.NewV7()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("session refresh: failed to generate refresh ID: %w", err)
	}
	familyID, err := uuid.NewV7()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("session refresh: failed to generate family ID: %w", err)
	}

	now := time.Now().UTC()
	refreshExp := now.Add(s.cfg.Security.RefreshTokenTTL)

	// [COMMENT]: 4. Chuẩn bị struct thực thể refresh token
	rt := iamEntity.RefreshToken{
		ID:            refreshID,
		UserID:        userID,
		DeviceID:      &deviceID,
		TokenHash:     security.HashTokenSHA256(rawRefresh),
		TokenFamilyID: familyID,
		TenantID:      nil,
		IssuedAt:      now,
		ExpiresAt:     refreshExp,
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

	// [COMMENT]: 8. Đọc thông tin Zone ID (mặc định global hoặc custom từ metadata)
	zoneID := "global"

	return &iamEntity.VerifyOpaqueRefreshTokenResult{
		Valid:    true,
		UserID:   user.ID.String(),
		TenantID: tenantIDStr,
		Role:     roleCode,
		Level:    int32(roleLevel),
		ZoneID:   zoneID,
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

