package iamSvcImpl

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	mailproto "controlplane/internal/mail/transport/rpc/proto"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	"controlplane/pkg/constant"
	"controlplane/pkg/id"

	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const (
	defaultLocale   = "vi-VN"
	defaultTimezone = "Asia/Ho_Chi_Minh"
)

type AuthService struct {
	repo       iamRepoInterface.AuthRepository
	rbacRepo   iamRepoInterface.RbacRepository
	refreshSvc iamSvcInterface.SessionRefreshService
	deviceSvc  iamSvcInterface.DeviceService
	registry   *cacheengine.CacheRegistry
	ott        iamSvcInterface.OneTimeTokenService
	outboxRepo iamRepoInterface.IamOutboxRepository
	cfg        *config.Config
	aclClient  iamproto.SessionServiceClient
}

func NewAuthService(cfg *config.Config,
	repo iamRepoInterface.AuthRepository,
	rbacRepo iamRepoInterface.RbacRepository,
	refreshSvc iamSvcInterface.SessionRefreshService,
	deviceSvc iamSvcInterface.DeviceService,
	registry *cacheengine.CacheRegistry,
	ott iamSvcInterface.OneTimeTokenService,
	outboxRepo iamRepoInterface.IamOutboxRepository,
	aclClient iamproto.SessionServiceClient,
) iamSvcInterface.AuthService {
	return &AuthService{
		repo:       repo,
		rbacRepo:   rbacRepo,
		refreshSvc: refreshSvc,
		deviceSvc:  deviceSvc,
		registry:   registry,
		ott:        ott,
		outboxRepo: outboxRepo,
		cfg:        cfg,
		aclClient:  aclClient,
	}
}

func (s *AuthService) RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) (err error) {
	result := iamMetrics.OutcomeSuccess
	defer func() {
		// Ghi nhận kết quả nghiệp vụ tổng thể của luồng đăng ký.
		iamMetrics.ServiceCall(ctx, result)
	}()

	// Presence cache chỉ là acceleration path. Đo lường latency & outcome của Redis check.
	presenceStart := time.Now()
	rdb := s.registry.L2.Client()
	var usernameHit, emailHit bool
	usernameDigest, cacheErr := security.PresenceHMACSHA256Hex("iam.register.username", user.Username)
	var emailDigest string
	if cacheErr == nil {
		emailDigest, cacheErr = security.PresenceHMACSHA256Hex("iam.register.email", user.Email)
	}
	var usernameHitInt, emailHitInt int64
	if cacheErr == nil {
		usernameHitInt, cacheErr = rdb.GetBit(ctx, "iam:register:bitmap:username", id.BitmapIndex(usernameDigest)).Result()
	}
	if cacheErr == nil {
		emailHitInt, cacheErr = rdb.GetBit(ctx, "iam:register:bitmap:email", id.BitmapIndex(emailDigest)).Result()
	}
	if cacheErr != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "checkPresence", iamMetrics.OutcomeFailureUnknown, time.Since(presenceStart), cacheErr)
	} else {
		usernameHit = usernameHitInt == 1
		emailHit = emailHitInt == 1
	}

	if cacheErr == nil && (usernameHit || emailHit) {
		// Cache hit nghi ngờ duplicate -> xác nhận lại ở DB (SoT).
		dbCheckStart := time.Now()
		exists, checkErr := s.repo.CheckUserExist(ctx, user.Username, user.Email)
		if checkErr != nil {
			result = iamMetrics.OutcomeFailureUnknown
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "CheckUserExist", iamMetrics.OutcomeFailureUnknown, time.Since(dbCheckStart), checkErr)
			return fmt.Errorf("%w: user check exist failed: %v", iamTaxonomy.ErrAuthenticationUnavailable, checkErr)
		}
		if exists {
			result = iamMetrics.OutcomePreConditionFailed
			return apperr.Wrap(iamTaxonomy.ErrUserAlreadyExist, nil, iamMetrics.OutcomePreConditionFailed)
		}
	}
	if cacheErr == nil && !(usernameHit || emailHit) {
	}

	// Đo lường thời gian băm mật khẩu để SRE theo dõi mức sử dụng CPU (CPU-bound).
	passwordHash, hashErr := security.HashPassword(password)
	if hashErr != nil {
		result = iamMetrics.OutcomeFailureUnknown
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, hashErr, iamMetrics.OutcomeFailureUnknown)
	}

	now := time.Now().UTC()
	userID, idErr := uuid.NewV7()
	if idErr != nil {
		result = iamMetrics.OutcomeFailureUnknown
		return fmt.Errorf("%w: failed to generate user ID: %v", iamTaxonomy.ErrAuthenticationUnavailable, idErr)
	}

	user.ID = userID
	user.PasswordHash = passwordHash
	user.Status = iamEntity.UserStatusPendingActive
	user.CreatedAt = now
	user.UpdatedAt = now

	profile.UserID = userID
	profile.AvatarURL = nil
	profile.Bio = nil
	if profile.Locale == "" {
		profile.Locale = defaultLocale
	}
	if profile.Timezone == "" {
		profile.Timezone = defaultTimezone
	}
	profile.CreatedAt = now
	profile.UpdatedAt = now

	// Thực hiện ghi dữ liệu xuống database và đo lường latency của transaction (I/O-bound).
	insertStart := time.Now()
	insertErr := s.repo.CreateRegisteredUser(ctx, user, profile)
	if insertErr != nil {
		// DB unique violation được map về domain duplicate ở repo; sau đó mark presence
		// best-effort để các request sau short-circuit sớm.
		if errors.Is(insertErr, iamTaxonomy.ErrUserAlreadyExist) {
			result = iamMetrics.OutcomePreConditionFailed
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "CreateRegisteredUser", iamMetrics.OutcomePreConditionFailed, time.Since(insertStart), insertErr)
			if usernameDigest, err := security.PresenceHMACSHA256Hex("iam.register.username", user.Username); err == nil {
				if emailDigest, err := security.PresenceHMACSHA256Hex("iam.register.email", user.Email); err == nil {
					pipe := rdb.Pipeline()
					pipe.SetBit(ctx, "iam:register:bitmap:username", id.BitmapIndex(usernameDigest), 1)
					pipe.SetBit(ctx, "iam:register:bitmap:email", id.BitmapIndex(emailDigest), 1)
					_, _ = pipe.Exec(ctx)
				}
			}
			return apperr.Wrap(iamTaxonomy.ErrUserAlreadyExist, insertErr, iamMetrics.OutcomePreConditionFailed)
		}
		result = iamMetrics.OutcomeFailureUnknown
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "CreateRegisteredUser", iamMetrics.OutcomeFailureUnknown, time.Since(insertStart), insertErr)
		return fmt.Errorf("%w: failed to create registered user: %v", iamTaxonomy.ErrAuthenticationUnavailable, insertErr)
	}

	// Đánh dấu presence trong cache (best-effort).
	markStart := time.Now()
	var markErr error
	usernameDigest, markErr = security.PresenceHMACSHA256Hex("iam.register.username", user.Username)
	if markErr == nil {
		emailDigest, markErr = security.PresenceHMACSHA256Hex("iam.register.email", user.Email)
	}
	if markErr == nil {
		pipe := rdb.Pipeline()
		pipe.SetBit(ctx, "iam:register:bitmap:username", id.BitmapIndex(usernameDigest), 1)
		pipe.SetBit(ctx, "iam:register:bitmap:email", id.BitmapIndex(emailDigest), 1)
		_, markErr = pipe.Exec(ctx)
	}
	if markErr != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "markPresenceExists", iamMetrics.OutcomeFailureUnknown, time.Since(markStart), markErr)
	}

	return nil
}

func (s *AuthService) Login(ctx context.Context, req iamEntity.LoginRequest) (err error) {
	var ipVal, uaVal string
	if v, ok := ctx.Value(constant.RemoteIPKey).(string); ok {
		ipVal = v
	}
	if v, ok := ctx.Value(constant.UserAgentKey).(string); ok {
		uaVal = v
	}

	loginOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		iamMetrics.ServiceCall(ctx, loginOutcome)
	}()

	// Repo load user là SoT; no fallback source khác cho identity.
	now := time.Now()
	user, loadErr := s.repo.GetLoginUserByUsername(ctx, req.Username)
	if loadErr != nil {
		if errors.Is(loadErr, iamTaxonomy.ErrInvalidCredentials) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetLoginUserByUsername", iamMetrics.OutcomeInvalidCredential, time.Since(now), loadErr)
			return apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, loadErr, iamMetrics.OutcomeInvalidCredential)
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetLoginUserByUsername", iamMetrics.OutcomeFailureUnknown, time.Since(now), loadErr)
		return fmt.Errorf("%w: failed to get login user: %v", iamTaxonomy.ErrAuthenticationUnavailable, loadErr)
	}

	verified, verifyErr := security.VerifyPassword(user.PasswordHash, req.Password)
	if verifyErr != nil {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, verifyErr, iamMetrics.OutcomeInvalidCredential)
	}
	if !verified {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}

	// Check user status
	switch user.Status {
	case iamEntity.UserStatusPendingActive:
		// [COMMENT]: Hỗ trợ chạy các test suite bằng cách kiểm tra nil cho mock dependency
		if s.ott == nil || s.outboxRepo == nil {
			loginOutcome = iamMetrics.OutcomePreConditionFailed
			return apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamMetrics.OutcomePreConditionFailed)
		}
		// Khởi tạo token OTT để gửi kèm mail xác thực
		verificationToken, _, issueErr := s.ott.Issue(ctx, "account_verify", user.ID)
		if issueErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, issueErr, iamMetrics.OutcomeFailureUnknown)
		}
		idempotencyKey := uuid.Must(uuid.NewV7())

		// Trích xuất Trace ID dạng nhị phân 16-byte từ context để tối ưu hóa lưu trữ cột BYTEA trong DB
		var traceID []byte
		if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
			tid := spanCtx.TraceID()
			traceID = tid[:]
		}

		// Đóng gói payload gửi mail bằng generic protobuf SendMailConfig
		// [COMMENT]: Sử dụng user.Username thay thế cho user.Fullname do login flow không truy vấn bảng profile nhằm tối ưu hóa hiệu năng
		mailConfig := &mailproto.SendMailConfig{
			To:       user.Email,
			Subject:  "Kích hoạt tài khoản của bạn",
			BodyHtml: "Vui lòng sử dụng token sau để kích hoạt tài khoản của bạn.",
			TemplateVariables: map[string]string{
				"fullname":     user.Username,
				"verify_token": verificationToken,
				"purpose":      "account_verify",
			},
		}

		// Tuần tự hóa cấu hình sang dạng nhị phân Protobuf
		payloadBytes, marshalErr := proto.Marshal(mailConfig)
		if marshalErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return fmt.Errorf("%w: failed to marshal verification mail config: %v", iamTaxonomy.ErrAuthenticationUnavailable, marshalErr)
		}

		// Khởi tạo thực thể bản ghi IamOutboxRecord để lưu xuống DB
		record := &iamEntity.IamOutboxRecord{
			EventID:              idempotencyKey,
			RoutingScope:         "platform",
			JobTopic:             "mail.system.verify_account",
			Payload:              payloadBytes,
			UserID:               user.ID.String(),
			Status:               iamEntity.IamOutboxStatusPending,
			JobVersion:           1,
			ResourceID:           "verify_account",
			PayloadSchemaVersion: 1,
			TraceID:              traceID,
			Idle:                 60, // Hạn mức thời gian thực thi job 60 giây
		}

		// Thực hiện lưu trữ bền vững outbox record trong cùng luồng
		if insertErr := s.outboxRepo.Create(ctx, record); insertErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return fmt.Errorf("%w: failed to create iam outbox record: %v", iamTaxonomy.ErrAuthenticationUnavailable, insertErr)
		}

		loginOutcome = iamMetrics.OutcomePreConditionFailed
		return apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamMetrics.OutcomePreConditionFailed)

	case iamEntity.UserStatusSuspended, iamEntity.UserStatusDisabled:
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)

	case iamEntity.UserStatusActive:
		// Active user: proceed to login

	default:
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}

	// [COMMENT]: Phân giải thiết bị active từ userID và DevicePublicKey của trình duyệt gửi lên.
	// Không quan tâm cookie cũ (hoàn toàn tránh được client fake cookie hoặc cookie đã bị thu hồi).
	matchedClientDeviceID, err := s.deviceSvc.GetActiveDeviceID(ctx, user.ID, req.DevicePublicKey)

	var clientDeviceID string

	isNewDevice := false
	// [COMMENT]: Nếu không tìm thấy thiết bị active (hoặc thiết bị trước đó đã bị revoked) -> Xem như thiết bị mới hoàn toàn, tự sinh mới.
	if err != nil || matchedClientDeviceID == "" {
		newDeviceID := uuid.New()
		clientDeviceID = newDeviceID.String()
		isNewDevice = true
	} else {
		clientDeviceID = matchedClientDeviceID
	}

	deviceName := req.DeviceName
	if strings.TrimSpace(deviceName) == "" {
		deviceName = "unknown device"
	}
	deviceType := "browser"
	fp := sha256.Sum256([]byte(req.DevicePublicKey))
	fingerprint := hex.EncodeToString(fp[:])

	loginDevice := iamEntity.Device{
		UserID:               user.ID,
		DeviceName:           deviceName,
		DeviceType:           &deviceType,
		PublicKey:            req.DevicePublicKey,
		PublicKeyAlg:         security.AlgEd25519,
		PublicKeyFingerprint: fingerprint,
		ClientDeviceID:       cleanOptionalString(&clientDeviceID),
		LastSeenIP:           cleanOptionalString(&ipVal),
		LastSeenUserAgent:    cleanOptionalString(&uaVal),
		UpdatedAt:            now.UTC(),
	}

	// RegisterLoginDevice qua DeviceService là DB SoT để lấy tracked device persistent.
	trackedDevice, deviceErr := s.deviceSvc.RegisterLoginDevice(ctx, loginDevice)
	if deviceErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return fmt.Errorf("%w: failed to upsert login device: %v", iamTaxonomy.ErrAuthenticationUnavailable, deviceErr)
	}
	if trackedDevice == nil || strings.TrimSpace(trackedDevice.ID) == "" || trackedDevice.Status == iamEntity.DeviceStatusRevoked {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}
	trackedDeviceID, trackedErr := uuid.Parse(strings.TrimSpace(trackedDevice.ID))
	if trackedErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return fmt.Errorf("%w: failed to parse tracked device ID: %v", iamTaxonomy.ErrAuthenticationUnavailable, trackedErr)
	}

	// [COMMENT]: Chỉ cấp refresh token khi người dùng chọn "trusted device in 30 days".
	// Nếu không tin tưởng thiết bị, chỉ cấp access token với TTL 30 phút – hết hạn là logout.
	var rawRefresh string
	if req.TrustDevice {
		var refreshErr error
		// [COMMENT]: Ủy quyền hoàn toàn việc tạo và lưu trữ session refresh token dạng <userID>_<entropy> cho SessionRefreshService.
		// AuthService chỉ nhận lại kết quả token thô và thời điểm hết hạn.
		rawRefresh, _, refreshErr = s.refreshSvc.CreateRefreshToken(ctx, user.ID, trackedDeviceID)
		if refreshErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return refreshErr
		}
	}

	// [COMMENT]: Dùng GetUserRoleAndLevelByScope với scope là platform (không có tenant)
	role, levelInt, err := s.rbacRepo.GetUserRoleAndLevelByScope(ctx, user.ID, "platform")
	if err != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}
	level := int32(levelInt)
	tenantID := ""

	// [COMMENT]: Gọi ACL gRPC Service để khởi tạo Trinity credentials, tạo JWT & lưu phiên vào L2 Redis.
	// Refresh token truyền trực tiếp trong proto request, không qua metadata header.
	aclResp, aclErr := s.aclClient.ReleaseTrinitySession(ctx, &iamproto.ReleaseTrinitySessionRequest{
		UserId:         user.ID.String(),
		DeviceId:       trackedDeviceID.String(),
		ClientDeviceId: clientDeviceID,
		Username:       user.Username,
		Role:           role,
		Level:          level,
		TrustDevice:    req.TrustDevice,
		ClientIp:       ipVal,
		UserAgent:      uaVal,
		TenantId:       tenantID,
		ZoneId:         req.ZoneCode,
		RefreshToken:   rawRefresh,
	})
	if aclErr != nil || aclResp == nil || !aclResp.Released {
		// [COMMENT]: Nếu gRPC write lỗi và đã cấp refresh token ở PostgreSQL, rollback bằng revoke để tránh token mồ côi.
		if req.TrustDevice {
			_ = s.refreshSvc.RevokeRefreshTokensByDeviceIDAndUserID(ctx, user.ID, trackedDeviceID)
		}
		// [COMMENT]: Rollback device mới tạo nếu có lỗi/thất bại sinh phiên
		if isNewDevice {
			ident := &constant.Identity{UserID: user.ID.String()}
			ctxWithIdent := context.WithValue(ctx, constant.IdentityKey, ident)
			_ = s.deviceSvc.RevokeMyDevice(ctxWithIdent, trackedDeviceID)
		}
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		if aclErr != nil {
			return fmt.Errorf("%w: failed to issue trinity session from ACL service: %v", iamTaxonomy.ErrAuthenticationUnavailable, aclErr)
		}
		return fmt.Errorf("%w: failed to release trinity session from ACL service", iamTaxonomy.ErrAuthenticationUnavailable)
	}

	// Best-effort cap reconcile sau login. Không làm fail request thành công.
	s.deviceSvc.EvictExcessDevicesIfNeeded(ctx, user.ID)

	return nil
}

// normalizeUserDevicePublicKey decode base64 (std hoặc raw) ed25519 public key
// (32 bytes) và trả canonical form base64 std để repo lưu + so sánh fingerprint.
func normalizeUserDevicePublicKey(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("empty key")
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil {
		return "", err
	}
	if len(decoded) != ed25519.PublicKeySize {
		return "", fmt.Errorf("invalid key size")
	}
	return base64.StdEncoding.EncodeToString(decoded), nil
}

func cleanOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	cleaned := strings.TrimSpace(*value)
	if cleaned == "" {
		return nil
	}
	return &cleaned
}

func cleanString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// VerifyUserTrinitySession xác thực thông tin đăng nhập của End-User thông thường qua gRPC
func (s *AuthService) VerifyUserTrinitySession(ctx context.Context, token string, accessKey string, accessSecret string) (*iamEntity.VerifySessionResult, error) {
	// [COMMENT]: Giải mã JWT trực tiếp bằng Vault (không dùng candidates từ DB)
	claims, parseErr := security.Parse(token, nil)
	if parseErr != nil {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	// Bước 3: Đối chiếu access_key trong token với access_key client cung cấp
	if strings.TrimSpace(claims.AccessKey) == "" || claims.AccessKey != accessKey {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	// Bước 4: Kiểm tra tính hoạt động của session từ Redis trực tiếp
	// [COMMENT]: Sử dụng Redis client trực tiếp (không qua L2 wrapper) vì Login Service
	// ghi session bằng rdb.Set(ctx, key, payload) — L2 wrapper transform key thành "{key}:data" gây mismatch.
	sessionKey := "iam:user_access_session:" + claims.Subject + ":" + accessKey
	rdb := s.registry.L2.Client()
	rawResult, redisErr := rdb.Get(ctx, sessionKey).Result()
	if redisErr != nil {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	var pb iamproto.UserAccessSession
	err := proto.Unmarshal([]byte(rawResult), &pb)
	if err != nil {
		return &iamEntity.VerifySessionResult{Valid: false}, err
	}
	ash := pb.Ash

	// So sánh SHA256 hash của access_secret nhận được
	incomingHash := security.HashTokenSHA256(accessSecret)
	if ash != incomingHash {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	return &iamEntity.VerifySessionResult{
		Valid:  true,
		UserID: claims.Subject,
		Role:   claims.Role,
		ZoneID: claims.ZoneID,
	}, nil
}

func (s *AuthService) VerifyOpaqueRefreshToken(ctx context.Context, refreshToken string, scope string) (*iamEntity.VerifyOpaqueRefreshTokenResult, error) {
	// [COMMENT]: Ủy thác (delegate) hoàn toàn logic xác thực Opaque Refresh Token sang cho SessionRefreshService
	return s.refreshSvc.VerifyOpaqueRefreshToken(ctx, refreshToken, scope)
}
