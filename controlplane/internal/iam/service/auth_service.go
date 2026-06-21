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
	"controlplane/internal/http/middleware"
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
	refreshSvc iamSvcInterface.SessionRefreshService
	deviceSvc  iamSvcInterface.DeviceService
	registry   *cacheengine.CacheRegistry
	ott        iamSvcInterface.OneTimeTokenService
	outboxRepo iamRepoInterface.IamOutboxRepository
	cfg        *config.Config
}

func NewAuthService(cfg *config.Config,
	repo iamRepoInterface.AuthRepository,
	refreshSvc iamSvcInterface.SessionRefreshService,
	deviceSvc iamSvcInterface.DeviceService,
	registry *cacheengine.CacheRegistry,
	ott iamSvcInterface.OneTimeTokenService,
	outboxRepo iamRepoInterface.IamOutboxRepository,
) iamSvcInterface.AuthService {
	return &AuthService{
		repo:       repo,
		refreshSvc: refreshSvc,
		deviceSvc:  deviceSvc,
		registry:   registry,
		ott:        ott,
		outboxRepo: outboxRepo,
		cfg:        cfg,
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

func (s *AuthService) Login(ctx context.Context, req iamEntity.LoginRequest) (result *iamEntity.LoginResult, err error) {
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
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, loadErr, iamMetrics.OutcomeInvalidCredential)
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetLoginUserByUsername", iamMetrics.OutcomeFailureUnknown, time.Since(now), loadErr)
		return nil, fmt.Errorf("%w: failed to get login user: %v", iamTaxonomy.ErrAuthenticationUnavailable, loadErr)
	}

	verified, verifyErr := security.VerifyPassword(user.PasswordHash, req.Password)
	if verifyErr != nil {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, verifyErr, iamMetrics.OutcomeInvalidCredential)
	}
	if !verified {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}

	// Check user status
	switch user.Status {
	case iamEntity.UserStatusPendingActive:
		// Pending-active: trigger verification side effect theo policy ở callsite.
		// Nếu OTT/outboxRepo config thiếu -> degrade thành VerificationRequired.
		if s.ott == nil || s.outboxRepo == nil {
			loginOutcome = iamMetrics.OutcomePreConditionFailed
			return nil, apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamMetrics.OutcomePreConditionFailed)
		}

		// Khởi tạo token OTT để gửi kèm mail xác thực
		verificationToken, _, issueErr := s.ott.Issue(ctx, "account_verify", user.ID)
		if issueErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, issueErr, iamMetrics.OutcomeFailureUnknown)
		}
		idempotencyKey := uuid.Must(uuid.NewV7())

		// Trích xuất Trace ID dạng nhị phân 16-byte từ context để tối ưu hóa lưu trữ cột BYTEA trong DB
		var traceID []byte
		if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
			tid := spanCtx.TraceID()
			traceID = tid[:]
		}

		// Trích xuất Zone ID từ context để phân vùng multi-tenant
		zoneID, ok := middleware.GetZoneID(ctx)
		if !ok || zoneID == uuid.Nil {
			zoneID = uuid.Nil
		}

		// Đóng gói payload gửi mail bằng generic protobuf SendMailConfig
		mailConfig := &mailproto.SendMailConfig{
			To:       user.Email,
			Subject:  "Kích hoạt tài khoản của bạn",
			BodyHtml: "Vui lòng sử dụng token sau để kích hoạt tài khoản của bạn.",
			TemplateVariables: map[string]string{
				"fullname":     user.Fullname,
				"verify_token": verificationToken,
				"purpose":      "account_verify",
			},
		}

		// Tuần tự hóa cấu hình sang dạng nhị phân Protobuf
		payloadBytes, marshalErr := proto.Marshal(mailConfig)
		if marshalErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return nil, fmt.Errorf("%w: failed to marshal verification mail config: %v", iamTaxonomy.ErrAuthenticationUnavailable, marshalErr)
		}

		// Khởi tạo thực thể bản ghi IamOutboxRecord để lưu xuống DB
		record := &iamEntity.IamOutboxRecord{
			EventID:              idempotencyKey,
			ZoneID:               zoneID,
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
			return nil, fmt.Errorf("%w: failed to create iam outbox record: %v", iamTaxonomy.ErrAuthenticationUnavailable, insertErr)
		}

		loginOutcome = iamMetrics.OutcomePreConditionFailed
		return nil, apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamMetrics.OutcomePreConditionFailed)

	case iamEntity.UserStatusSuspended, iamEntity.UserStatusDisabled:
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)

	case iamEntity.UserStatusActive:
		// Active user: proceed to login

	default:
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}

	// Runtime cache chỉ phục vụ flush heuristic. Cache read lỗi thì fallback DB path.
	clientDeviceProvenance := "client"
	if req.ClientDeviceID == uuid.Nil {
		// [COMMENT]: Gọi deviceSvc để phân giải (resolve) client_device_id từ public key của thiết bị gửi lên.
		// Tránh việc gọi trực tiếp repository ở tầng auth service nhằm giữ đúng ranh giới kiến trúc (architectural boundaries).
		matchedClientDeviceID, resolveErr := s.deviceSvc.ResolveClientDeviceID(ctx, user.ID, req.DevicePublicKey)
		if resolveErr == nil && matchedClientDeviceID != "" {
			parsedID, parseErr := uuid.Parse(matchedClientDeviceID)
			if parseErr == nil {
				req.ClientDeviceID = parsedID
				clientDeviceProvenance = "server-recovery"
			}
		}

		// [COMMENT]: Chỉ sinh mới client_device_id khi đây là thiết bị mới chưa từng đăng ký khóa trước đó.
		if req.ClientDeviceID == uuid.Nil {
			req.ClientDeviceID = uuid.New()
			clientDeviceProvenance = "server-bootstrap"
		}
	}
	clientDeviceID := req.ClientDeviceID.String()

	rdb := s.registry.L2.Client()
	userIDStr := user.ID.String()
	indexKey := "iam:user_access_index:" + userIDStr

	// RegisterLoginDevice qua DeviceService là DB SoT để lấy tracked device persistent.
	trackedDevice, deviceErr := s.deviceSvc.RegisterLoginDevice(ctx, buildLoginDevice(user.ID, req.DevicePublicKey, req.IP, req.UserAgent, now, req.DeviceName, clientDeviceID))
	if deviceErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to upsert login device: %v", iamTaxonomy.ErrAuthenticationUnavailable, deviceErr)
	}
	if trackedDevice == nil || strings.TrimSpace(trackedDevice.ID) == "" || trackedDevice.Status == iamEntity.DeviceStatusRevoked {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}
	trackedDeviceID, trackedErr := uuid.Parse(strings.TrimSpace(trackedDevice.ID))
	if trackedErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to parse tracked device ID: %v", iamTaxonomy.ErrAuthenticationUnavailable, trackedErr)
	}

	// Runtime fragment generation: lỗi ở đây là auth dependency error (fail-close).
	accessKeyID, accessKeyErr := uuid.NewV7()
	if accessKeyErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to generate access key: %v", iamTaxonomy.ErrAuthenticationUnavailable, accessKeyErr)
	}
	accessKey := accessKeyID.String()
	accessSecret, secretErr := security.GenerateToken(32)
	if secretErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to generate access secret: %v", iamTaxonomy.ErrAuthenticationUnavailable, secretErr)
	}
	accessJTI, idErr := uuid.NewV7()
	if idErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to generate access JTI: %v", iamTaxonomy.ErrAuthenticationUnavailable, idErr)
	}
	accessExp := now.Add(s.cfg.Security.AccessSecretTTL)
	// Access token chứa accessKey + jti để middleware verify với runtime cache.
	accessToken, accessErr := security.SignWithSecret(security.Claims{
		Subject:   user.ID.String(),
		Role:      "",
		Level:     0,
		AccessKey: accessKey,
		TokenID:   accessJTI.String(),
		TokenUse:  "access",
		IssuedAt:  now.Unix(),
		ExpiresAt: accessExp.Unix(),
	}, nil)
	if accessErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to sign access token: %v", iamTaxonomy.ErrAuthenticationUnavailable, accessErr)
	}

	// [COMMENT]: Chỉ cấp refresh token khi người dùng chọn "trusted device in 30 days".
	// Nếu không tin tưởng thiết bị, chỉ cấp access token với TTL 30 phút – hết hạn là logout.
	var rawRefresh string
	var refreshExp time.Time
	if req.TrustDevice {
		var refreshErr error
		// Ủy quyền hoàn toàn việc tạo và lưu trữ session refresh token cho SessionRefreshService.
		// AuthService chỉ nhận lại kết quả token thô và thời điểm hết hạn.
		rawRefresh, refreshExp, refreshErr = s.refreshSvc.CreateUserOpaqueSession(ctx, user.ID, trackedDeviceID)
		if refreshErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return nil, refreshErr
		}
	}

	// Runtime write lỗi: rollback theo callsite policy bằng revoke refresh token.
	// Không fallback chạy tiếp vì sẽ tạo access token không verify được runtime.
	sessionRecord := iamEntity.UserAccessSession{
		AccessSecretHash: security.HashTokenSHA256(accessSecret),
		TrackedDeviceID:  trackedDeviceID.String(),
		LastSeenAt:       now.Unix(), // Middleware sẽ cập nhật lsa realtime sau mỗi request xác thực
	}
	var setErr error
	pb := &iamproto.UserAccessSession{
		Ash:  sessionRecord.AccessSecretHash,
		Tdid: sessionRecord.TrackedDeviceID,
		Lsa:  sessionRecord.LastSeenAt,
	}
	if payload, marshalErr := proto.Marshal(pb); marshalErr != nil {
		setErr = marshalErr
	} else {
		key := "iam:user_access_session:" + userIDStr + ":" + accessKey
		pipe := rdb.TxPipeline()
		pipe.Set(ctx, key, payload, s.cfg.Security.AccessSecretTTL)
		pipe.SAdd(ctx, indexKey, accessKey)
		pipe.Expire(ctx, indexKey, s.cfg.Security.AccessSecretTTL+24*time.Hour)
		_, setErr = pipe.Exec(ctx)
	}
	if setErr != nil {
		// [COMMENT]: Nếu runtime write lỗi và đã cấp refresh token, rollback bằng revoke để tránh token mồ côi.
		if req.TrustDevice && s.refreshSvc != nil {
			_ = s.refreshSvc.RevokeRefreshTokensByDeviceIDAndUserID(ctx, user.ID, trackedDeviceID)
		}
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to set device runtime: %v", iamTaxonomy.ErrAuthenticationUnavailable, setErr)
	}

	// Best-effort cap reconcile sau login. Không làm fail request thành công.
	s.deviceSvc.EvictExcessDevicesIfNeeded(ctx, user.ID, req.IP, req.UserAgent)

	return &iamEntity.LoginResult{
		AccessToken:              accessToken,
		RefreshToken:             rawRefresh,
		AccessKey:                accessKey,
		AccessSecret:             accessSecret,
		TrackedDeviceID:          trackedDeviceID.String(),
		ClientDeviceID:           clientDeviceID,
		ClientDeviceIDProvenance: string(clientDeviceProvenance),
		AccessExpiresAt:          accessExp,
		RefreshExpiresAt:         refreshExp,
	}, nil
}

func buildLoginDevice(userID uuid.UUID, devicePublicKey string, ip *string, userAgent *string, now time.Time, deviceName string, clientDeviceID string) iamEntity.Device {
	if strings.TrimSpace(deviceName) == "" {
		deviceName = "unknown device"
	}
	deviceType := "browser"
	fp := sha256.Sum256([]byte(devicePublicKey))
	return iamEntity.Device{
		UserID:               userID,
		DeviceName:           deviceName,
		DeviceType:           &deviceType,
		PublicKey:            devicePublicKey,
		PublicKeyAlg:         security.AlgEd25519,
		PublicKeyFingerprint: hex.EncodeToString(fp[:]),
		ClientDeviceID:       cleanOptionalString(&clientDeviceID),
		LastSeenIP:           cleanOptionalString(ip),
		LastSeenUserAgent:    cleanOptionalString(userAgent),
		UpdatedAt:            now.UTC(),
	}
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

func (s *AuthService) Logout(ctx context.Context) error {
	var userIDStr string
	var accessKey string
	if ident, ok := ctx.Value(constant.IdentityKey).(*constant.Identity); ok && ident != nil {
		userIDStr = ident.UserID
		accessKey = ident.AccessKey
	}
	userIDStr = strings.TrimSpace(userIDStr)
	accessKey = strings.TrimSpace(accessKey)
	if userIDStr == "" || accessKey == "" {
		return fmt.Errorf("%w: missing userID or access key in context", iamTaxonomy.ErrInvalidArgument)
	}
	userID, parseErr := uuid.Parse(userIDStr)
	if parseErr != nil {
		return fmt.Errorf("%w: invalid user id in context: %v", iamTaxonomy.ErrInvalidArgument, parseErr)
	}

	rdb := s.registry.L2.Client()

	// 1. Đọc runtime record để lấy trackedDeviceID + dirty state trước khi xoá.
	var runtimeRecord *iamproto.UserAccessSession
	key := "iam:user_access_session:" + userIDStr + ":" + accessKey
	if raw, getErr := rdb.Get(ctx, key).Result(); getErr == nil {
		var record iamproto.UserAccessSession
		if proto.Unmarshal([]byte(raw), &record) == nil {
			runtimeRecord = &record
		}
	}

	// 2. PHẾ BỎ PHIÊN LÀM VIỆC NGAY LẬP TỨC (SECURITY CRITICAL)
	// Xoá runtime key khỏi Redis → access middleware check tiếp theo sẽ miss → 401 tức thì.
	indexKey := "iam:user_access_index:" + userIDStr
	pipe := rdb.TxPipeline()
	pipe.Del(ctx, key)
	pipe.SRem(ctx, indexKey, accessKey)
	_, _ = pipe.Exec(ctx)

	// 3. CẬP NHẬT DB BẤT ĐỒNG BỘ (best-effort, không block response)
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		if runtimeRecord != nil && strings.TrimSpace(runtimeRecord.Tdid) != "" {
			if deviceUUID, parseErr := uuid.Parse(runtimeRecord.Tdid); parseErr == nil {
				_ = s.refreshSvc.RevokeRefreshTokensByDeviceIDAndUserID(bgCtx, userID, deviceUUID)
			}
		} else {
			_ = s.refreshSvc.RevokeRefreshTokensByUserID(bgCtx, userID, nil)
		}
	}()

	return nil
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
