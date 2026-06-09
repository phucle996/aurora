package iamSvcImpl

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	infraredis "controlplane/infra/redis"
	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	iamCache "controlplane/internal/iam/cache"
	deviceHint "controlplane/internal/iam/devicehint"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	defaultLocale   = "vi-VN"
	defaultTimezone = "Asia/Ho_Chi_Minh"
)

type AuthService struct {
	repo             iamRepoInterface.AuthRepository
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository
	deviceRepo       iamRepoInterface.DeviceRepository
	deviceRuntime    iamCache.UserDeviceRuntimeCache
	capLock          iamCache.UserDeviceCapLock
	presence         iamCache.RegisterPresenceCache
	registry         *cacheengine.CacheRegistry
	ott              iamSvcInterface.OneTimeTokenService
	streamPublisher  infraredis.StreamPublisher
	cfg              *config.Config
}

func NewAuthService(cfg *config.Config,
	repo iamRepoInterface.AuthRepository,
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository,
	deviceRepo iamRepoInterface.DeviceRepository,
	deviceRuntime iamCache.UserDeviceRuntimeCache,
	capLock iamCache.UserDeviceCapLock,
	presence iamCache.RegisterPresenceCache,
	registry *cacheengine.CacheRegistry,
	ott iamSvcInterface.OneTimeTokenService,
	streamPublisher infraredis.StreamPublisher,
) iamSvcInterface.AuthService {
	return &AuthService{
		repo:             repo,
		refreshTokenRepo: refreshTokenRepo,
		deviceRepo:       deviceRepo,
		deviceRuntime:    deviceRuntime,
		capLock:          capLock,
		presence:         presence,
		registry:         registry,
		ott:              ott,
		streamPublisher:  streamPublisher,
		cfg:              cfg,
	}
}

func (s *AuthService) RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) (err error) {
	startedAt := time.Now()
	result := iamTaxonomy.OutcomeSuccess
	cachePath := iamTaxonomy.RegisterCachePathMiss
	defer func() {
		// Ghi nhận kết quả nghiệp vụ tổng thể của luồng đăng ký.
		iamMetrics.ObserveServiceCall("register", result, cachePath)
		// Đo lường tổng thời gian thực hiện luồng (I/O + CPU).
		iamMetrics.ObserveDownstream("db", "total", time.Since(startedAt), err)
	}()

	user.Username = strings.TrimSpace(user.Username)
	user.Email = strings.TrimSpace(user.Email)
	profile.Fullname = strings.TrimSpace(profile.Fullname)
	password = strings.TrimSpace(password)

	// Presence cache chỉ là acceleration path. Đo lường latency & outcome của Redis check.
	presenceStart := time.Now()
	usernameHit, emailHit, cacheErr := s.presence.Check(ctx, user.Username, user.Email)
	iamMetrics.ObserveDownstream("redis", "presence_check", time.Since(presenceStart), cacheErr)

	if cacheErr != nil {
		cachePath = iamTaxonomy.RegisterCachePathFallback
	}

	if cacheErr == nil && (usernameHit || emailHit) {
		// Cache hit nghi ngờ duplicate -> xác nhận lại ở DB (SoT).
		cachePath = iamTaxonomy.RegisterCachePathHitDBCheck
		dbCheckStart := time.Now()
		exists, checkErr := s.repo.CheckUserExist(ctx, user.Username, user.Email)
		iamMetrics.ObserveDownstream("db", "exist_check", time.Since(dbCheckStart), checkErr)
		if checkErr != nil {
			result = iamTaxonomy.RegisterOutcomeExistCheckError
			return fmt.Errorf("%w: user check exist failed: %v", iamTaxonomy.ErrAuthenticationUnavailable, checkErr)
		}
		if exists {
			result = iamTaxonomy.RegisterOutcomeAlreadyExists
			return apperr.Wrap(iamTaxonomy.ErrUserAlreadyExist, nil, iamTaxonomy.RegisterOutcomeAlreadyExists)
		}
	}
	if cacheErr == nil && !(usernameHit || emailHit) {
		cachePath = iamTaxonomy.RegisterCachePathMiss
	}

	// Đo lường thời gian băm mật khẩu để SRE theo dõi mức sử dụng CPU (CPU-bound).
	cryptoStart := time.Now()
	passwordHash, hashErr := security.HashPassword(password)
	iamMetrics.ObserveDownstream("crypto", "hash_password", time.Since(cryptoStart), hashErr)
	if hashErr != nil {
		result = iamTaxonomy.RegisterOutcomeArgon2HashFailed
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, hashErr, iamTaxonomy.RegisterOutcomeArgon2HashFailed)
	}

	now := time.Now().UTC()
	userID, idErr := uuid.NewV7()
	if idErr != nil {
		result = iamTaxonomy.RegisterOutcomeIDGenerateErr
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
	iamMetrics.ObserveDownstream("db", "insert_user", time.Since(insertStart), insertErr)
	if insertErr != nil {
		// DB unique violation được map về domain duplicate ở repo; sau đó mark presence
		// best-effort để các request sau short-circuit sớm.
		if errors.Is(insertErr, iamTaxonomy.ErrUserAlreadyExist) {
			result = iamTaxonomy.RegisterOutcomeAlreadyExists
			_ = s.presence.MarkExists(ctx, user.Username, user.Email)
			return apperr.Wrap(iamTaxonomy.ErrUserAlreadyExist, insertErr, iamTaxonomy.RegisterOutcomeAlreadyExists)
		}
		result = iamTaxonomy.RegisterOutcomeInsertError
		return fmt.Errorf("%w: failed to create registered user: %v", iamTaxonomy.ErrAuthenticationUnavailable, insertErr)
	}

	// Đánh dấu presence trong cache (best-effort).
	markStart := time.Now()
	markErr := s.presence.MarkExists(ctx, user.Username, user.Email)
	iamMetrics.ObserveDownstream("redis", "presence_mark", time.Since(markStart), markErr)

	return nil
}

func (s *AuthService) Login(ctx context.Context, req iamEntity.LoginRequest) (result *iamEntity.LoginResult, err error) {
	startedAt := time.Now()
	loginOutcome := iamTaxonomy.OutcomeSuccess
	defer func() {
		iamMetrics.ObserveServiceCall("login", loginOutcome, "n/a")
		iamMetrics.ObserveDownstream("db", "login_total", time.Since(startedAt), err)
	}()

	// Repo load user là SoT; no fallback source khác cho identity.
	user, loadErr := s.repo.GetLoginUserByUsername(ctx, req.Username)
	if loadErr != nil {
		if errors.Is(loadErr, pgx.ErrNoRows) {
			loginOutcome = iamTaxonomy.LoginOutcomeInvalidCredentials
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, loadErr, iamTaxonomy.LoginOutcomeInvalidCredentials)
		}
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to get login user: %v", iamTaxonomy.ErrAuthenticationUnavailable, loadErr)
	}
	if user == nil {
		loginOutcome = iamTaxonomy.LoginOutcomeInvalidCredentials
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamTaxonomy.LoginOutcomeInvalidCredentials)
	}

	verified, verifyErr := security.VerifyPassword(user.PasswordHash, req.Password)
	if verifyErr != nil {
		loginOutcome = iamTaxonomy.LoginOutcomeInvalidCredentials
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, verifyErr, iamTaxonomy.LoginOutcomeInvalidCredentials)
	}
	if !verified {
		loginOutcome = iamTaxonomy.LoginOutcomeInvalidCredentials
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamTaxonomy.LoginOutcomeInvalidCredentials)
	}

	// Pending-active: trigger verification side effect theo policy ở callsite.
	// Nếu OTT/publisher config thiếu -> degrade thành VerificationRequired.
	if user.Status == iamEntity.UserStatusPendingActive {
		if s.ott == nil || s.streamPublisher == nil {
			loginOutcome = iamTaxonomy.LoginOutcomeVerificationReq
			return nil, apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamTaxonomy.LoginOutcomeVerificationReq)
		}
		verificationToken, _, issueErr := s.ott.Issue(ctx, "account_verify", user.ID)
		if issueErr != nil {
			loginOutcome = iamTaxonomy.OutcomeFailure
			return nil, fmt.Errorf("%w: failed to issue verification token: %v", iamTaxonomy.ErrAuthenticationUnavailable, issueErr)
		}
		idempotencyKey := uuid.Must(uuid.NewV7())

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
		iamMetrics.ObserveDownstream("redis", "publish_verify_mail", time.Since(publishStartedAt), publishErr)
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
		if publishErr != nil {
			span.RecordError(publishErr)
			span.SetStatus(codes.Error, publishErr.Error())
			span.End()
			loginOutcome = iamTaxonomy.OutcomeFailure
			return nil, fmt.Errorf("%w: failed to publish verification mail job: %v", iamTaxonomy.ErrAuthenticationUnavailable, publishErr)
		}
		span.End()
		loginOutcome = iamTaxonomy.LoginOutcomeVerificationReq
		return nil, apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamTaxonomy.LoginOutcomeVerificationReq)
	}
	if user.Status == iamEntity.UserStatusSuspended || user.Status == iamEntity.UserStatusDisabled {
		return nil, iamTaxonomy.ErrInvalidCredentials
	}

	// Runtime cache chỉ phục vụ flush heuristic. Cache read lỗi thì fallback DB path.
	deviceName := deviceHint.ResolveDeviceName(req.HostnameHint, req.HostnameAlias)
	clientDeviceID, clientDeviceProvenance := deviceHint.ResolveClientDeviceID(req.ClientDeviceID)
	flushLabel := "db"
	if s.deviceRuntime != nil {
		// Cache lookup lỗi/empty -> giữ flushLabel=db, không fail login.
		if runtimeCandidates, scanErr := s.deviceRuntime.ScanByUser(ctx, user.ID.String(), 1); scanErr == nil {
			for _, candidate := range runtimeCandidates {
				if strings.TrimSpace(candidate.UserID) != user.ID.String() {
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
		}
	}

	now := time.Now().UTC()

	// UpsertLoginDevice là DB SoT để lấy tracked device persistent.
	trackedDevice, deviceErr := s.deviceRepo.UpsertLoginDevice(ctx, buildLoginDevice(user.ID, req.DevicePublicKey, req.IP, req.UserAgent, now, deviceName, clientDeviceID))
	iamMetrics.ObserveServiceCall("login_last_seen_flush", flushLabel, "n/a")
	if deviceErr != nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to upsert login device: %v", iamTaxonomy.ErrAuthenticationUnavailable, deviceErr)
	}
	if trackedDevice == nil || strings.TrimSpace(trackedDevice.ID) == "" || trackedDevice.Status == iamEntity.DeviceStatusRevoked {
		loginOutcome = iamTaxonomy.LoginOutcomeInvalidCredentials
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamTaxonomy.LoginOutcomeInvalidCredentials)
	}
	trackedDeviceID, trackedErr := uuid.Parse(strings.TrimSpace(trackedDevice.ID))
	if trackedErr != nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to parse tracked device ID: %v", iamTaxonomy.ErrAuthenticationUnavailable, trackedErr)
	}

	// Runtime fragment generation: lỗi ở đây là auth dependency error (fail-close).
	accessKeyID, accessKeyErr := uuid.NewV7()
	if accessKeyErr != nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to generate access key: %v", iamTaxonomy.ErrAuthenticationUnavailable, accessKeyErr)
	}
	accessKey := accessKeyID.String()
	accessSecret, secretErr := security.GenerateToken(32)
	if secretErr != nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to generate access secret: %v", iamTaxonomy.ErrAuthenticationUnavailable, secretErr)
	}
	accessJTI, idErr := uuid.NewV7()
	if idErr != nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to generate access JTI: %v", iamTaxonomy.ErrAuthenticationUnavailable, idErr)
	}
	accessExp := now.Add(s.cfg.Security.AccessSecretTTL)
	val, err := s.registry.GetOrLoad(ctx, "access_secret", "")
	if err != nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to get access secret from registry: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	secrets, ok := val.(*coreEntity.RuntimeSecrets)
	if !ok || secrets == nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
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
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to sign access token: %v", iamTaxonomy.ErrAuthenticationUnavailable, accessErr)
	}

	rawRefresh, refreshErr := security.GenerateToken(43)
	if refreshErr != nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to generate refresh token: %v", iamTaxonomy.ErrAuthenticationUnavailable, refreshErr)
	}
	refreshID, refreshIDErr := uuid.NewV7()
	if refreshIDErr != nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to generate refresh ID: %v", iamTaxonomy.ErrAuthenticationUnavailable, refreshIDErr)
	}
	familyID, familyErr := uuid.NewV7()
	if familyErr != nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
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
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to persist refresh session: %v", iamTaxonomy.ErrAuthenticationUnavailable, persistErr)
	}

	// Runtime write lỗi: rollback theo callsite policy bằng revoke refresh token.
	// Không fallback chạy tiếp vì sẽ tạo access token không verify được runtime.
	runtime := iamCache.UserDeviceRuntime{
		DeviceID:         accessKey,
		DeviceSecretHash: security.HashTokenSHA256(accessSecret),
		CurrentJTI:       accessJTI.String(),
		TrackedDeviceID:  trackedDeviceID.String(),
		UserID:           user.ID.String(),
		Status:           "online",
		Version:          1,
		LastSeenAt:       now.Unix(),
		CurrentIssuedAt:  now.Unix(),
	}
	if req.IP != nil {
		runtime.LastSeenIP = strings.TrimSpace(*req.IP)
	}
	if req.UserAgent != nil {
		runtime.LastSeenUserAgent = strings.TrimSpace(*req.UserAgent)
	}
	if setErr := s.deviceRuntime.SetDeviceRuntime(ctx, runtime, s.cfg.Security.AccessSecretTTL); setErr != nil {
		if s.refreshTokenRepo != nil {
			_, _ = s.refreshTokenRepo.RevokeRefreshTokensByDeviceIDAndUserID(ctx, user.ID, trackedDeviceID)
		}
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: failed to set device runtime: %v", iamTaxonomy.ErrAuthenticationUnavailable, setErr)
	}

	// Best-effort cap reconcile sau login. Không làm fail request thành công.
	s.evictExcessDevicesIfNeeded(ctx, user.ID, req.IP, req.UserAgent)

	return &iamEntity.LoginResult{
		AccessToken:              accessToken,
		RefreshToken:             rawRefresh,
		RuntimeDeviceID:          accessKey,
		DeviceSecret:             accessSecret,
		TrackedDeviceID:          trackedDeviceID.String(),
		ClientDeviceID:           clientDeviceID,
		ClientDeviceIDProvenance: string(clientDeviceProvenance),
		AccessExpiresAt:          accessExp,
		RefreshExpiresAt:         refreshExp,
	}, nil
}

func buildLoginDevice(userID uuid.UUID, devicePublicKey string, ip *string, userAgent *string, now time.Time, deviceName string, clientDeviceID string) iamEntity.Device {
	if strings.TrimSpace(deviceName) == "" {
		deviceName = deviceHint.DefaultDeviceName
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

	// 1. Đọc runtime record để lấy trackedDeviceID + dirty state trước khi xoá.
	var runtimeRecord *iamCache.UserDeviceRuntime
	if s.deviceRuntime != nil {
		runtimeRecord, _ = s.deviceRuntime.GetDeviceRuntimeByUserDevice(ctx, userID.String(), accessKey)
	}

	// 2. PHẾ BỎ PHIÊN LÀM VIỆC NGAY LẬP TỨC (SECURITY CRITICAL)
	// Xoá runtime key khỏi Redis → access middleware check tiếp theo sẽ miss → 401 tức thì.
	if s.deviceRuntime != nil {
		_ = s.deviceRuntime.DeleteDeviceRuntimeByUserDevice(ctx, userID.String(), accessKey)
	}

	// 3. CẬP NHẬT DB BẤT ĐỒNG BỘ (best-effort, không block response)
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		// Revoke refresh tokens theo device scope nếu có, fallback revoke toàn user.
		if runtimeRecord != nil && strings.TrimSpace(runtimeRecord.TrackedDeviceID) != "" {
			if deviceUUID, parseErr := uuid.Parse(runtimeRecord.TrackedDeviceID); parseErr == nil {
				_, _ = s.refreshTokenRepo.RevokeRefreshTokensByDeviceIDAndUserID(bgCtx, userID, deviceUUID)
			}
		} else {
			_, _ = s.refreshTokenRepo.RevokeRefreshTokensByUserID(bgCtx, userID, nil)
		}

		// Flush last_seen xuống DB nếu runtime có dirty state (giống AdminLogout).
		if runtimeRecord != nil && runtimeRecord.LastSeenDirty && strings.TrimSpace(runtimeRecord.TrackedDeviceID) != "" {
			if s.deviceRepo != nil {
				if deviceUUID, parseErr := uuid.Parse(runtimeRecord.TrackedDeviceID); parseErr == nil {
					ip := optionalStringPointer(runtimeRecord.LastSeenIP)
					ua := optionalStringPointer(runtimeRecord.LastSeenUserAgent)
					_ = s.deviceRepo.TouchDeviceLastSeen(bgCtx, deviceUUID, ip, ua)
				}
			}
		}
	}()

	return nil
}

const userDeviceCap = 50

func (s *AuthService) evictExcessDevicesIfNeeded(ctx context.Context, userID uuid.UUID, ip *string, userAgent *string) {
	// Best-effort maintenance path: mọi lỗi ở đây không được làm fail login.
	if s == nil || s.deviceRepo == nil {
		return
	}
	if s.capLock != nil {
		// Fallback policy tại callsite:
		// - lock backend lỗi: chạy tiếp không lock (degrade)
		// - lock contention: skip vì worker khác đang xử lý.
		lockToken, ok, lockErr := s.capLock.TryAcquire(ctx, userID.String(), 2*time.Second)
		if lockErr != nil {
			// Fallback policy ở callsite: lock backend lỗi thì degrade về best-effort
			// (chạy evict không lock) thay vì fail cứng login flow.
			iamMetrics.ObserveServiceCall("device_cap_lock", "skip", "n/a")
		} else if !ok {
			// Lock contention: worker khác đang xử lý cap flow cho user này.
			iamMetrics.ObserveServiceCall("device_cap_lock", "skip", "n/a")
			return
		} else {
			defer func() { _ = s.capLock.Release(ctx, userID.String(), lockToken) }()
		}
	}
	evicted, err := s.deviceRepo.EvictExcessDevices(ctx, userID, userDeviceCap)
	if err != nil || len(evicted) == 0 {
		return
	}
	deviceIDs := make([]uuid.UUID, 0, len(evicted))
	for _, item := range evicted {
		deviceIDs = append(deviceIDs, item.DeviceID)
	}
	if s.refreshTokenRepo != nil {
		_, _ = s.refreshTokenRepo.RevokeRefreshTokensByDeviceIDsAndUserID(ctx, userID, deviceIDs)
	}
	if s.deviceRuntime != nil {
		// Runtime cleanup là side-effect best-effort sau DB evict thành công.
		runtimes, scanErr := s.deviceRuntime.ScanByUser(ctx, userID.String(), 200)
		if scanErr == nil {
			evictedRefs := make(map[string]struct{}, len(evicted))
			for _, item := range evicted {
				evictedRefs[strings.TrimSpace(item.DeviceID.String())] = struct{}{}
			}
			for _, rt := range runtimes {
				if _, found := evictedRefs[strings.TrimSpace(rt.TrackedDeviceID)]; found {
					_ = s.deviceRuntime.DeleteDeviceRuntimeByUserDevice(ctx, rt.UserID, rt.DeviceID)
				}
			}
		}
	}
	iamMetrics.ObserveServiceCall("device_cap_evict", "evicted", "n/a")
	extras := map[string]string{
		"reason":        "cap_exceeded",
		"evicted_count": strconv.Itoa(len(evicted)),
	}
	s.publishDeviceAuditAsync(ctx, userID, "device.evicted_capacity", "warning", ip, userAgent, extras)
}

// ReconcileDeviceCap được gọi định kỳ bởi background worker để vá drift do
// lock skip ở login flow. Idempotent: rerun trên user đã đúng cap không gây
// side effect (CTE OFFSET trả 0 row).
func (s *AuthService) ReconcileDeviceCap(ctx context.Context, batch int) (int, error) {
	if s == nil || s.deviceRepo == nil {
		return 0, nil
	}
	if batch <= 0 {
		batch = 100
	}
	users, err := s.deviceRepo.ListUsersExceedingDeviceCap(ctx, userDeviceCap, batch)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, uid := range users {
		s.evictExcessDevicesIfNeeded(ctx, uid, nil, nil)
		processed++
	}
	return processed, nil
}

// NewAuthServiceImpl trả pointer impl cho phép module gọi method ngoài interface
// (ví dụ ReconcileDeviceCap chạy ở background scheduler).
func NewAuthServiceImpl(cfg *config.Config,
	repo iamRepoInterface.AuthRepository,
	refreshTokenRepo iamRepoInterface.RefreshTokenRepository,
	deviceRepo iamRepoInterface.DeviceRepository,
	deviceRuntime iamCache.UserDeviceRuntimeCache,
	capLock iamCache.UserDeviceCapLock,
	presence iamCache.RegisterPresenceCache,
	registry *cacheengine.CacheRegistry,
	ott iamSvcInterface.OneTimeTokenService,
	streamPublisher infraredis.StreamPublisher,
) *AuthService {
	return &AuthService{
		repo:             repo,
		refreshTokenRepo: refreshTokenRepo,
		deviceRepo:       deviceRepo,
		deviceRuntime:    deviceRuntime,
		capLock:          capLock,
		presence:         presence,
		registry:         registry,
		ott:              ott,
		streamPublisher:  streamPublisher,
		cfg:              cfg,
	}
}

// WrapAuthService trả interface AuthService gói pointer impl.
func WrapAuthService(impl *AuthService) iamSvcInterface.AuthService { return impl }

// publishDeviceAuditAsync publish audit event vào Redis stream `iam:audit:device`.
// Fallback: nếu publish lỗi hoặc publisher chưa có, ghi DB qua deviceRepo.InsertAuditEvent.
// Idempotency dùng userID:event:nano để tránh duplicate khi retry.
func (s *AuthService) publishDeviceAuditAsync(ctx context.Context, userID uuid.UUID, event string, severity string, ip *string, userAgent *string, extras map[string]string) {
	if s == nil {
		return
	}
	if s.streamPublisher == nil {
		if s.deviceRepo != nil {
			_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, event, severity, ip, userAgent)
			iamMetrics.ObserveServiceCall("audit_publish", "fallback_db", "n/a")
		}
		return
	}
	now := time.Now().UTC()
	payload := map[string]string{
		"event":        event,
		"severity":     severity,
		"user_id":      userID.String(),
		"published_at": now.Format(time.RFC3339Nano),
	}
	if ip != nil {
		payload["ip"] = strings.TrimSpace(*ip)
	}
	if userAgent != nil {
		payload["user_agent"] = strings.TrimSpace(*userAgent)
	}
	for k, v := range extras {
		payload[k] = v
	}
	msg := infraredis.StreamMessage{
		Stream:         "iam:audit:device",
		IdempotencyKey: userID.String() + ":" + event,
		Payload:        payload,
	}
	_, _, err := s.streamPublisher.Publish(ctx, msg, 30*time.Second)
	if err != nil {
		if s.deviceRepo != nil {
			_ = s.deviceRepo.InsertAuditEvent(ctx, &userID, event, severity, ip, userAgent)
		}
		iamMetrics.ObserveServiceCall("audit_publish", "fallback_db", "n/a")
		return
	}
	iamMetrics.ObserveServiceCall("audit_publish", "published", "n/a")
}

func cleanString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
