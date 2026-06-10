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

	infraredis "controlplane/infra/redis"
	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	"controlplane/pkg/id"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	defaultLocale   = "vi-VN"
	defaultTimezone = "Asia/Ho_Chi_Minh"
)

type AuthService struct {
	repo            iamRepoInterface.AuthRepository
	refreshSvc      iamSvcInterface.RefreshTokenService
	deviceSvc       iamSvcInterface.DeviceService
	registry        *cacheengine.CacheRegistry
	ott             iamSvcInterface.OneTimeTokenService
	streamPublisher infraredis.StreamPublisher
	cfg             *config.Config
}

func NewAuthService(cfg *config.Config,
	repo iamRepoInterface.AuthRepository,
	refreshSvc iamSvcInterface.RefreshTokenService,
	deviceSvc iamSvcInterface.DeviceService,
	registry *cacheengine.CacheRegistry,
	ott iamSvcInterface.OneTimeTokenService,
	streamPublisher infraredis.StreamPublisher,
) iamSvcInterface.AuthService {
	return &AuthService{
		repo:            repo,
		refreshSvc:      refreshSvc,
		deviceSvc:       deviceSvc,
		registry:        registry,
		ott:             ott,
		streamPublisher: streamPublisher,
		cfg:             cfg,
	}
}

func (s *AuthService) RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) (err error) {
	result := iamTaxonomy.Success
	cachePath := "n/a"
	defer func() {
		// Ghi nhận kết quả nghiệp vụ tổng thể của luồng đăng ký.
		iamMetrics.ServiceCall("register", result, cachePath)
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
		cachePath = "cache_fallback"
		iamMetrics.Downstream("redis", "register", "checkPresence", iamTaxonomy.Failure, time.Since(presenceStart), cacheErr)
	} else {
		usernameHit = usernameHitInt == 1
		emailHit = emailHitInt == 1
	}

	if cacheErr == nil && (usernameHit || emailHit) {
		// Cache hit nghi ngờ duplicate -> xác nhận lại ở DB (SoT).
		cachePath = "cache_hit_db_check"
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
		cachePath = "cache_miss"
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
	cachePath := "n/a"
	defer func() {
		iamMetrics.ServiceCall(workflow, loginOutcome, cachePath)
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
		// Nếu OTT/publisher config thiếu -> degrade thành VerificationRequired.
		if s.ott == nil || s.streamPublisher == nil {
			loginOutcome = iamTaxonomy.PreConditionFailed
			return nil, apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamTaxonomy.PreConditionFailed)
		}

		verificationToken, _, issueErr := s.ott.Issue(ctx, "account_verify", user.ID)
		if issueErr != nil {
			loginOutcome = iamTaxonomy.Failure
			return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, issueErr, iamTaxonomy.Failure)
		}
		idempotencyKey := uuid.Must(uuid.NewV7())

		// mail job send verify email
		streamMsg := infraredis.StreamMessage{
			Stream:         "mail:jobs",
			IdempotencyKey: idempotencyKey.String(),
			Payload: map[string]string{
				"event_type":   "mail.verify_account.requested",
				"purpose":      "account_verify",
				"user_id":      user.ID.String(),
				"email":        user.Email,
				"fullname":     user.Fullname,
				"verify_token": verificationToken,
				"requested_at": time.Now().UTC().Format(time.RFC3339Nano),
				"request_id":   idempotencyKey.String(),
			},
		}
		// Publish fail -> fail login theo security policy, không fallback silent.
		publishStartedAt := time.Now()
		traceCtx, span := otel.Tracer("aurora-controlplane.iam").Start(ctx, "iam.login.publish_verify_mail_job")
		streamID, published, publishErr := s.streamPublisher.Publish(traceCtx, streamMsg, s.cfg.Security.OneTimeTokenTTL)
		if publishErr != nil {
			iamMetrics.Downstream("redis", "login", "Publish", iamTaxonomy.Failure, time.Since(publishStartedAt), publishErr)
			span.RecordError(publishErr)
			span.SetStatus(codes.Error, publishErr.Error())
			span.End()
			loginOutcome = iamTaxonomy.Failure
			return nil, fmt.Errorf("%w: failed to publish verification mail job: %v", iamTaxonomy.ErrAuthenticationUnavailable, publishErr)
		}
		span.SetAttributes(
			attribute.String("stream", streamMsg.Stream),
			attribute.String("event_type", streamMsg.Payload["event_type"]),
			attribute.String("purpose", streamMsg.Payload["purpose"]),
			attribute.String("user_id", streamMsg.Payload["user_id"]),
			attribute.String("idempotency_key", idempotencyKey.String()),
			attribute.Bool("published", published),
		)
		if streamID != "" {
			span.SetAttributes(attribute.String("stream_id", streamID))
		}
		span.End()
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
				if json.Unmarshal([]byte(rawStr), &record) == nil && record.UserID == userIDStr {
					runtimeCandidates = append(runtimeCandidates, record)
				}
			}
		}
	}
	for _, candidate := range runtimeCandidates {
		if strings.TrimSpace(candidate.UserID) != userIDStr {
			continue
		}
		if strings.TrimSpace(candidate.LastSeenIP) == cleanString(req.IP) &&
			strings.TrimSpace(candidate.LastSeenUserAgent) == cleanString(req.UserAgent) &&
			candidate.LastSeenAt > 0 &&
			time.Since(time.Unix(candidate.LastSeenAt, 0)) < 60*time.Second {
			flushLabel = "cache_hit"
			break
		}
	}

	// RegisterLoginDevice qua DeviceService là DB SoT để lấy tracked device persistent.
	trackedDevice, deviceErr := s.deviceSvc.RegisterLoginDevice(ctx, buildLoginDevice(user.ID, req.DevicePublicKey, req.IP, req.UserAgent, now, req.DeviceName, clientDeviceID))
	iamMetrics.ServiceCall("login_last_seen_flush", flushLabel, "n/a")
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
		AccessKey:        accessKey,
		AccessSecretHash: security.HashTokenSHA256(accessSecret),
		CurrentJTI:       accessJTI.String(),
		TrackedDeviceID:  trackedDeviceID.String(),
		UserID:           user.ID.String(),
		Status:           "online",
		Version:          1,
		LastSeenAt:       now.Unix(),
		CurrentIssuedAt:  now.Unix(),
	}
	if req.IP != nil {
		sessionRecord.LastSeenIP = strings.TrimSpace(*req.IP)
	}
	if req.UserAgent != nil {
		sessionRecord.LastSeenUserAgent = strings.TrimSpace(*req.UserAgent)
	}
	var setErr error
	if payload, marshalErr := json.Marshal(sessionRecord); marshalErr != nil {
		setErr = marshalErr
	} else {
		key := "iam:user_access_session:" + sessionRecord.UserID + ":" + sessionRecord.AccessKey
		pipe := rdb.TxPipeline()
		pipe.Set(ctx, key, payload, s.cfg.Security.AccessSecretTTL)
		pipe.SAdd(ctx, indexKey, sessionRecord.AccessKey)
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
		if json.Unmarshal([]byte(raw), &record) == nil && record.UserID == userIDStr {
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

		if s.refreshSvc != nil {
			if runtimeRecord != nil && strings.TrimSpace(runtimeRecord.TrackedDeviceID) != "" {
				if deviceUUID, parseErr := uuid.Parse(runtimeRecord.TrackedDeviceID); parseErr == nil {
					_ = s.refreshSvc.RevokeRefreshTokensByDeviceIDAndUserID(bgCtx, userID, deviceUUID)
				}
			} else {
				_ = s.refreshSvc.RevokeRefreshTokensByUserID(bgCtx, userID, nil)
			}
		}

		// Flush last_seen xuống DB nếu runtime có dirty state (giống AdminLogout).
		if runtimeRecord != nil && runtimeRecord.LastSeenDirty && strings.TrimSpace(runtimeRecord.TrackedDeviceID) != "" {
			if s.deviceSvc != nil {
				if deviceUUID, parseErr := uuid.Parse(runtimeRecord.TrackedDeviceID); parseErr == nil {
					ip := optionalStringPointer(runtimeRecord.LastSeenIP)
					ua := optionalStringPointer(runtimeRecord.LastSeenUserAgent)
					_ = s.deviceSvc.TouchDeviceLastSeen(bgCtx, deviceUUID, ip, ua)
				}
			}
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
