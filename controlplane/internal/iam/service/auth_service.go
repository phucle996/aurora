package iamSvcImpl

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	"controlplane/internal/http/middleware"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	mailproto "controlplane/internal/mail/transport/rpc/proto"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	"controlplane/pkg/id"

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
	refreshSvc iamSvcInterface.RefreshTokenService
	deviceSvc  iamSvcInterface.DeviceService
	registry   *cacheengine.CacheRegistry
	ott        iamSvcInterface.OneTimeTokenService
	outboxRepo iamRepoInterface.IamOutboxRepository
	cfg        *config.Config
}

func NewAuthService(cfg *config.Config,
	repo iamRepoInterface.AuthRepository,
	refreshSvc iamSvcInterface.RefreshTokenService,
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
	result := iamTaxonomy.Success
	defer func() {
		// Ghi nhận kết quả nghiệp vụ tổng thể của luồng đăng ký.
		iamMetrics.ServiceCall("register", result)
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
		iamMetrics.Downstream("redis", "register", "checkPresence", iamTaxonomy.Failure, time.Since(presenceStart), cacheErr)
	} else {
		usernameHit = usernameHitInt == 1
		emailHit = emailHitInt == 1
	}

	if cacheErr == nil && (usernameHit || emailHit) {
		// Cache hit nghi ngờ duplicate -> xác nhận lại ở DB (SoT).
		dbCheckStart := time.Now()
		exists, checkErr := s.repo.CheckUserExist(ctx, user.Username, user.Email)
		if checkErr != nil {
			result = iamTaxonomy.FailureUnknown
			iamMetrics.Downstream("db", "register", "CheckUserExist", iamTaxonomy.FailureUnknown, time.Since(dbCheckStart), checkErr)
			return fmt.Errorf("%w: user check exist failed: %v", iamTaxonomy.ErrAuthenticationUnavailable, checkErr)
		}
		if exists {
			result = iamTaxonomy.PreConditionFailed
			return apperr.Wrap(iamTaxonomy.ErrUserAlreadyExist, nil, iamTaxonomy.PreConditionFailed)
		}
	}
	if cacheErr == nil && !(usernameHit || emailHit) {
	}

	// Đo lường thời gian băm mật khẩu để SRE theo dõi mức sử dụng CPU (CPU-bound).
	passwordHash, hashErr := security.HashPassword(password)
	if hashErr != nil {
		result = iamTaxonomy.FailureUnknown
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, hashErr, iamTaxonomy.FailureUnknown)
	}

	now := time.Now().UTC()
	userID, idErr := uuid.NewV7()
	if idErr != nil {
		result = iamTaxonomy.FailureUnknown
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
			result = iamTaxonomy.PreConditionFailed
			iamMetrics.Downstream("db", "register", "CreateRegisteredUser", iamTaxonomy.Failure, time.Since(insertStart), insertErr)
			if usernameDigest, err := security.PresenceHMACSHA256Hex("iam.register.username", user.Username); err == nil {
				if emailDigest, err := security.PresenceHMACSHA256Hex("iam.register.email", user.Email); err == nil {
					pipe := rdb.Pipeline()
					pipe.SetBit(ctx, "iam:register:bitmap:username", id.BitmapIndex(usernameDigest), 1)
					pipe.SetBit(ctx, "iam:register:bitmap:email", id.BitmapIndex(emailDigest), 1)
					_, _ = pipe.Exec(ctx)
				}
			}
			return apperr.Wrap(iamTaxonomy.ErrUserAlreadyExist, insertErr, iamTaxonomy.PreConditionFailed)
		}
		result = iamTaxonomy.Failure
		iamMetrics.Downstream("db", "register", "CreateRegisteredUser", iamTaxonomy.FailureUnknown, time.Since(insertStart), insertErr)
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
		iamMetrics.Downstream("redis", "register", "markPresenceExists", iamTaxonomy.Failure, time.Since(markStart), markErr)
	}

	return nil
}

func (s *AuthService) Login(ctx context.Context, req iamEntity.LoginRequest) (result *iamEntity.LoginResult, err error) {
	const workflow = "login"
	loginOutcome := iamTaxonomy.Success
	defer func() {
		iamMetrics.ServiceCall(workflow, loginOutcome)
	}()

	// Repo load user là SoT; no fallback source khác cho identity.
	now := time.Now()
	user, loadErr := s.repo.GetLoginUserByUsername(ctx, req.Username)
	if loadErr != nil {
		if errors.Is(loadErr, iamTaxonomy.ErrInvalidCredentials) {
			iamMetrics.Downstream("repo", workflow, "GetLoginUserByUsername", iamTaxonomy.Failure, time.Since(now), loadErr)
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, loadErr, iamTaxonomy.InvalidCredential)
		}
		iamMetrics.Downstream("repo", workflow, "GetLoginUserByUsername", iamTaxonomy.FailureUnknown, time.Since(now), loadErr)
		return nil, fmt.Errorf("%w: failed to get login user: %v", iamTaxonomy.ErrAuthenticationUnavailable, loadErr)
	}

	verified, verifyErr := security.VerifyPassword(user.PasswordHash, req.Password)
	if verifyErr != nil {
		loginOutcome = iamTaxonomy.InvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, verifyErr, iamTaxonomy.InvalidCredential)
	}
	if !verified {
		loginOutcome = iamTaxonomy.InvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamTaxonomy.InvalidCredential)
	}

	// Check user status
	switch user.Status {
	case iamEntity.UserStatusPendingActive:
		// Pending-active: trigger verification side effect theo policy ở callsite.
		// Nếu OTT/outboxRepo config thiếu -> degrade thành VerificationRequired.
		if s.ott == nil || s.outboxRepo == nil {
			loginOutcome = iamTaxonomy.PreConditionFailed
			return nil, apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamTaxonomy.PreConditionFailed)
		}

		// Khởi tạo token OTT để gửi kèm mail xác thực
		verificationToken, _, issueErr := s.ott.Issue(ctx, "account_verify", user.ID)
		if issueErr != nil {
			loginOutcome = iamTaxonomy.Failure
			return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, issueErr, iamTaxonomy.Failure)
		}
		idempotencyKey := uuid.Must(uuid.NewV7())

		// Trích xuất Trace ID từ context để truyền nối tiếp vết (Distributed Tracing)
		var traceIDPtr *string
		if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
			tid := spanCtx.TraceID().String()
			traceIDPtr = &tid
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
			loginOutcome = iamTaxonomy.Failure
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
			TraceID:              traceIDPtr,
			Idle:                 60, // Hạn mức thời gian thực thi job 60 giây
		}

		// Thực hiện lưu trữ bền vững outbox record trong cùng luồng
		if insertErr := s.outboxRepo.Create(ctx, record); insertErr != nil {
			loginOutcome = iamTaxonomy.Failure
			return nil, fmt.Errorf("%w: failed to create iam outbox record: %v", iamTaxonomy.ErrAuthenticationUnavailable, insertErr)
		}

		loginOutcome = iamTaxonomy.PreConditionFailed
		return nil, apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamTaxonomy.PreConditionFailed)

	case iamEntity.UserStatusSuspended, iamEntity.UserStatusDisabled:
		loginOutcome = iamTaxonomy.InvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamTaxonomy.InvalidCredential)

	case iamEntity.UserStatusActive:
		// Active user: proceed to login

	default:
		loginOutcome = iamTaxonomy.InvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamTaxonomy.InvalidCredential)
	}

	// Runtime cache chỉ phục vụ flush heuristic. Cache read lỗi thì fallback DB path.
	clientDeviceProvenance := "client"
	if req.ClientDeviceID == uuid.Nil {
		req.ClientDeviceID = uuid.New()
		clientDeviceProvenance = "server-bootstrap"
	}
	clientDeviceID := req.ClientDeviceID.String()
	flushLabel := "db"
	rdb := s.registry.L2.Client()
	userIDStr := user.ID.String()
	indexKey := "iam:user_access_index:" + userIDStr
	var runtimeCandidates []iamEntity.UserAccessSession
	if scanned, _, scanErr := rdb.SScan(ctx, indexKey, 0, "*", 1).Result(); scanErr == nil && len(scanned) > 0 {
		keys := []string{"iam:user_access_session:" + userIDStr + ":" + scanned[0]}
		if values, mgetErr := rdb.MGet(ctx, keys...).Result(); mgetErr == nil && len(values) > 0 && values[0] != nil {
			if rawStr, ok := values[0].(string); ok {
				var record iamEntity.UserAccessSession
				if json.Unmarshal([]byte(rawStr), &record) == nil {
					runtimeCandidates = append(runtimeCandidates, record)
				}
			}
		}
	}
	for _, candidate := range runtimeCandidates {
		// Nếu có session active trong vòng 60s → cùng thiết bị đang thao tác liên tục → skip flush DB.
		if candidate.LastSeenAt > 0 && time.Since(time.Unix(candidate.LastSeenAt, 0)) < 60*time.Second {
			flushLabel = "cache_hit"
			break
		}
	}

	// RegisterLoginDevice qua DeviceService là DB SoT để lấy tracked device persistent.
	trackedDevice, deviceErr := s.deviceSvc.RegisterLoginDevice(ctx, buildLoginDevice(user.ID, req.DevicePublicKey, req.IP, req.UserAgent, now, req.DeviceName, clientDeviceID))
	iamMetrics.ServiceCall("login_last_seen_flush", flushLabel)
	if deviceErr != nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: failed to upsert login device: %v", iamTaxonomy.ErrAuthenticationUnavailable, deviceErr)
	}
	if trackedDevice == nil || strings.TrimSpace(trackedDevice.ID) == "" || trackedDevice.Status == iamEntity.DeviceStatusRevoked {
		loginOutcome = iamTaxonomy.InvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamTaxonomy.InvalidCredential)
	}
	trackedDeviceID, trackedErr := uuid.Parse(strings.TrimSpace(trackedDevice.ID))
	if trackedErr != nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: failed to parse tracked device ID: %v", iamTaxonomy.ErrAuthenticationUnavailable, trackedErr)
	}

	// Runtime fragment generation: lỗi ở đây là auth dependency error (fail-close).
	accessKeyID, accessKeyErr := uuid.NewV7()
	if accessKeyErr != nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: failed to generate access key: %v", iamTaxonomy.ErrAuthenticationUnavailable, accessKeyErr)
	}
	accessKey := accessKeyID.String()
	accessSecret, secretErr := security.GenerateToken(32)
	if secretErr != nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: failed to generate access secret: %v", iamTaxonomy.ErrAuthenticationUnavailable, secretErr)
	}
	accessJTI, idErr := uuid.NewV7()
	if idErr != nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: failed to generate access JTI: %v", iamTaxonomy.ErrAuthenticationUnavailable, idErr)
	}
	accessExp := now.Add(s.cfg.Security.AccessSecretTTL)
	val, err := s.registry.GetOrLoad(ctx, "access_secret", "")
	if err != nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: failed to get access secret from registry: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	secrets, ok := val.(*coreEntity.RuntimeSecrets)
	if !ok || secrets == nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: invalid runtime secrets type", iamTaxonomy.ErrAuthenticationUnavailable)
	}

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
	}, secrets.Active.Secret)
	if accessErr != nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: failed to sign access token: %v", iamTaxonomy.ErrAuthenticationUnavailable, accessErr)
	}

	rawRefresh, refreshErr := security.GenerateToken(43)
	if refreshErr != nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: failed to generate refresh token: %v", iamTaxonomy.ErrAuthenticationUnavailable, refreshErr)
	}
	refreshID, refreshIDErr := uuid.NewV7()
	if refreshIDErr != nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: failed to generate refresh ID: %v", iamTaxonomy.ErrAuthenticationUnavailable, refreshIDErr)
	}
	familyID, familyErr := uuid.NewV7()
	if familyErr != nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: failed to generate family ID: %v", iamTaxonomy.ErrAuthenticationUnavailable, familyErr)
	}
	refreshExp := now.Add(s.cfg.Security.RefreshTokenTTL)

	// Persist refresh session trước khi ghi runtime để tránh token mồ côi.
	rt := &iamEntity.RefreshToken{
		ID:            refreshID,
		UserID:        user.ID,
		DeviceID:      &trackedDeviceID,
		TokenHash:     security.HashTokenSHA256(rawRefresh),
		TokenFamilyID: familyID,
		TenantID:      nil,
		IssuedAt:      now,
		ExpiresAt:     refreshExp,
	}
	if persistErr := s.repo.CreateRefreshTokenSession(ctx, *rt); persistErr != nil {
		loginOutcome = iamTaxonomy.Failure
		return nil, fmt.Errorf("%w: failed to persist refresh session: %v", iamTaxonomy.ErrAuthenticationUnavailable, persistErr)
	}

	// Runtime write lỗi: rollback theo callsite policy bằng revoke refresh token.
	// Không fallback chạy tiếp vì sẽ tạo access token không verify được runtime.
	sessionRecord := iamEntity.UserAccessSession{
		AccessSecretHash: security.HashTokenSHA256(accessSecret),
		TrackedDeviceID:  trackedDeviceID.String(),
		LastSeenAt:       now.Unix(), // Middleware sẽ cập nhật lsa realtime sau mỗi request xác thực
	}
	var setErr error
	if payload, marshalErr := json.Marshal(sessionRecord); marshalErr != nil {
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
		if s.refreshSvc != nil {
			_ = s.refreshSvc.RevokeRefreshTokensByDeviceIDAndUserID(ctx, user.ID, trackedDeviceID)
		}
		loginOutcome = iamTaxonomy.Failure
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

func (s *AuthService) Logout(ctx context.Context, userID uuid.UUID, accessKey string, accessSecret string) error {
	accessKey = strings.TrimSpace(accessKey)
	if accessKey == "" {
		return fmt.Errorf("%w: missing access key", iamTaxonomy.ErrInvalidArgument)
	}

	rdb := s.registry.L2.Client()
	userIDStr := userID.String()

	// 1. Đọc runtime record để lấy trackedDeviceID + dirty state trước khi xoá.
	var runtimeRecord *iamEntity.UserAccessSession
	key := "iam:user_access_session:" + userIDStr + ":" + accessKey
	if raw, getErr := rdb.Get(ctx, key).Result(); getErr == nil {
		var record iamEntity.UserAccessSession
		if json.Unmarshal([]byte(raw), &record) == nil {
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

		if runtimeRecord != nil && strings.TrimSpace(runtimeRecord.TrackedDeviceID) != "" {
			if deviceUUID, parseErr := uuid.Parse(runtimeRecord.TrackedDeviceID); parseErr == nil {
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

// VerifyAdminTrinitySession xác thực thông tin đăng nhập của Admin/SRE qua gRPC
func (s *AuthService) VerifyAdminTrinitySession(ctx context.Context, token string, accessKey string, accessSecret string) (*iamEntity.VerifySessionResult, error) {
	// [ignoring loop detection]
	// Bước 1: Truy xuất danh sách key ký mã hóa cho Admin từ cache registry
	val, err := s.registry.GetOrLoad(ctx, "admin_api_key", "")
	if err != nil {
		return &iamEntity.VerifySessionResult{Valid: false}, err
	}
	secrets, ok := val.(*coreEntity.RuntimeSecrets)
	if !ok || secrets == nil {
		return &iamEntity.VerifySessionResult{Valid: false}, fmt.Errorf("invalid runtime secrets type for admin")
	}

	// Bước 2: Giải mã JWT lần lượt bằng active rồi standby key
	var claims security.Claims
	parsed := false
	for _, candidate := range []coreEntity.RuntimeSecret{secrets.Active, secrets.Standby} {
		parsedClaims, parseErr := security.Parse(token, candidate.Secret)
		if parseErr == nil {
			claims = parsedClaims
			parsed = true
			break
		}
	}
	if !parsed {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	// Bước 3: Đối chiếu access_key trong token với access_key client cung cấp
	if strings.TrimSpace(claims.AccessKey) == "" || claims.AccessKey != accessKey {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	// Bước 4: Kiểm tra tính hoạt động của session từ Redis L2 cache
	payload, _, exists, err := s.registry.L2.Get(ctx, "admin_access_session:"+accessKey)
	if err != nil || !exists {
		return &iamEntity.VerifySessionResult{Valid: false}, err
	}

	var session struct {
		AccessSecretHash string `json:"access_secret_hash"`
	}
	if err := json.Unmarshal(payload, &session); err != nil {
		return &iamEntity.VerifySessionResult{Valid: false}, err
	}

	// So sánh SHA256 hash của access_secret nhận được
	incomingHash := security.HashTokenSHA256(accessSecret)
	if session.AccessSecretHash != incomingHash {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	return &iamEntity.VerifySessionResult{
		Valid:  true,
		UserID: claims.Subject,
		Role:   "SRE",
	}, nil
}

// VerifyUserTrinitySession xác thực thông tin đăng nhập của End-User thông thường qua gRPC
func (s *AuthService) VerifyUserTrinitySession(ctx context.Context, token string, accessKey string, accessSecret string) (*iamEntity.VerifySessionResult, error) {
	// [ignoring loop detection]
	// Bước 1: Truy xuất danh sách key ký mã hóa cho User từ cache registry
	val, err := s.registry.GetOrLoad(ctx, "access_secret", "")
	if err != nil {
		return &iamEntity.VerifySessionResult{Valid: false}, err
	}
	secrets, ok := val.(*coreEntity.RuntimeSecrets)
	if !ok || secrets == nil {
		return &iamEntity.VerifySessionResult{Valid: false}, fmt.Errorf("invalid runtime secrets type for user")
	}

	// Bước 2: Giải mã JWT lần lượt bằng active rồi standby key
	var claims security.Claims
	parsed := false
	for _, candidate := range []coreEntity.RuntimeSecret{secrets.Active, secrets.Standby} {
		parsedClaims, parseErr := security.Parse(token, candidate.Secret)
		if parseErr == nil {
			claims = parsedClaims
			parsed = true
			break
		}
	}
	if !parsed {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	// Bước 3: Đối chiếu access_key trong token với access_key client cung cấp
	if strings.TrimSpace(claims.AccessKey) == "" || claims.AccessKey != accessKey {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	// Bước 4: Kiểm tra tính hoạt động của session từ Redis L2 cache
	sessionKey := "iam:user_access_session:" + claims.Subject + ":" + accessKey
	payload, _, exists, err := s.registry.L2.Get(ctx, sessionKey)
	if err != nil || !exists {
		return &iamEntity.VerifySessionResult{Valid: false}, err
	}

	var session struct {
		AccessSecretHash string `json:"access_secret_hash"`
	}
	if err := json.Unmarshal(payload, &session); err != nil {
		return &iamEntity.VerifySessionResult{Valid: false}, err
	}

	// So sánh SHA256 hash của access_secret nhận được
	incomingHash := security.HashTokenSHA256(accessSecret)
	if session.AccessSecretHash != incomingHash {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	return &iamEntity.VerifySessionResult{
		Valid:  true,
		UserID: claims.Subject,
		Role:   claims.Role,
		ZoneID: claims.ZoneID,
	}, nil
}
