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
	"controlplane/internal/config"
	iamCache "controlplane/internal/iam/cache"
	deviceHint "controlplane/internal/iam/devicehint"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
		iamMetrics.ObserveRegisterOutcome(result, cachePath)
		iamMetrics.ObserveRegisterDB("total", time.Since(startedAt), err)
	}()

	// Callsite validate trước khi chạm cache/DB để fail-fast và giảm load dependency.
	if user.Username == "" || user.Email == "" || profile.Fullname == "" || password == "" {
		result = iamTaxonomy.RegisterOutcomeInvalidArgument
		cachePath = iamTaxonomy.RegisterCachePathNotChecked
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, iamTaxonomy.RegisterOutcomeInvalidArgument)
	}

	// Presence cache chỉ là acceleration path. Cache lỗi thì fallback ở callsite:
	// vẫn đi DB flow bình thường, không fail cứng register.
	cacheStartedAt := time.Now()
	usernameHit, emailHit, cacheErr := s.presence.Check(ctx, user.Username, user.Email)
	iamMetrics.ObserveRegisterRedis("presence_check", time.Since(cacheStartedAt), cacheErr)
	if cacheErr != nil {
		cachePath = iamTaxonomy.RegisterCachePathFallback
	}

	if cacheErr == nil && (usernameHit || emailHit) {
		// Cache hit nghi ngờ duplicate -> xác nhận lại ở DB (SoT).
		cachePath = iamTaxonomy.RegisterCachePathHitDBCheck
		dbCheckStartedAt := time.Now()
		exists, checkErr := s.repo.CheckUserExist(ctx, user.Username, user.Email)
		iamMetrics.ObserveRegisterDB("exist_check", time.Since(dbCheckStartedAt), checkErr)
		if checkErr != nil {
			result = iamTaxonomy.OutcomeFailure
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

	// Input đã validate ở callsite; từ đây là dependency operations.
	passwordHash, hashErr := security.HashPassword(password)
	if hashErr != nil {
		result = iamTaxonomy.OutcomeFailure
		return fmt.Errorf("%w: failed to hash password: %v", iamTaxonomy.ErrAuthenticationUnavailable, hashErr)
	}

	now := time.Now().UTC()
	userID, idErr := uuid.NewV7()
	if idErr != nil {
		result = iamTaxonomy.OutcomeFailure
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

	insertStartedAt := time.Now()
	insertErr := s.repo.CreateRegisteredUser(ctx, user, profile)
	iamMetrics.ObserveRegisterDB("insert", time.Since(insertStartedAt), insertErr)
	if insertErr != nil {
		// DB unique violation được map về domain duplicate; sau đó mark presence
		// best-effort để các request sau short-circuit sớm.
		var pgErr *pgconn.PgError
		if errors.As(insertErr, &pgErr) && pgErr.Code == "23505" {
			constraint := strings.ToLower(pgErr.ConstraintName)
			if strings.Contains(constraint, "users_email_lower_uidx") || strings.Contains(constraint, "users_username_lower_uidx") {
				result = iamTaxonomy.RegisterOutcomeAlreadyExists
				markStartedAt := time.Now()
				markErr := s.presence.MarkExists(ctx, user.Username, user.Email)
				iamMetrics.ObserveRegisterRedis("presence_mark_duplicate", time.Since(markStartedAt), markErr)
				return apperr.Wrap(iamTaxonomy.ErrUserAlreadyExist, insertErr, iamTaxonomy.RegisterOutcomeAlreadyExists)
			}
		}
		if errors.Is(insertErr, iamTaxonomy.ErrUserAlreadyExist) {
			result = iamTaxonomy.RegisterOutcomeAlreadyExists
			markStartedAt := time.Now()
			markErr := s.presence.MarkExists(ctx, user.Username, user.Email)
			iamMetrics.ObserveRegisterRedis("presence_mark_duplicate", time.Since(markStartedAt), markErr)
			return apperr.Wrap(iamTaxonomy.ErrUserAlreadyExist, insertErr, iamTaxonomy.RegisterOutcomeAlreadyExists)
		}
		result = iamTaxonomy.OutcomeFailure
		return fmt.Errorf("%w: failed to create registered user: %v", iamTaxonomy.ErrAuthenticationUnavailable, insertErr)
	}

	markStartedAt := time.Now()
	markErr := s.presence.MarkExists(ctx, user.Username, user.Email)
	iamMetrics.ObserveRegisterRedis("presence_mark_success", time.Since(markStartedAt), markErr)

	return nil
}

func (s *AuthService) Login(ctx context.Context, req iamEntity.LoginRequest) (result *iamEntity.LoginResult, err error) {
	startedAt := time.Now()
	loginOutcome := iamTaxonomy.OutcomeSuccess
	defer func() {
		iamMetrics.ObserveLoginOutcome(loginOutcome)
		iamMetrics.ObserveRegisterDB("login_total", time.Since(startedAt), err)
	}()

	// Validate credentials/input ở callsite trước khi đụng repo/cache.
	username := strings.TrimSpace(req.Username)
	password := strings.TrimSpace(req.Password)
	if username == "" || password == "" {
		loginOutcome = iamTaxonomy.LoginOutcomeInvalidCredentials
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamTaxonomy.LoginOutcomeInvalidCredentials)
	}
	// Device public key là contract bắt buộc của login flow.
	devicePublicKey := strings.TrimSpace(req.DevicePublicKey)
	if devicePublicKey == "" {
		loginOutcome = iamTaxonomy.LoginOutcomeInvalidArgument
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, iamTaxonomy.LoginOutcomeInvalidArgument)
	}
	// Callsite normalize/decode key để repo chỉ nhận canonical form.
	canonicalDevicePublicKey, devicePublicKeyErr := normalizeUserDevicePublicKey(devicePublicKey)
	if devicePublicKeyErr != nil {
		loginOutcome = iamTaxonomy.LoginOutcomeInvalidDevicePubKey
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, devicePublicKeyErr, iamTaxonomy.LoginOutcomeInvalidDevicePubKey)
	}

	// Repo load user là SoT; no fallback source khác cho identity.
	user, loadErr := s.repo.GetLoginUserByUsername(ctx, username)
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

	verified, verifyErr := security.VerifyPassword(user.PasswordHash, password)
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
		if s.ott == nil || s.streamPublisher == nil || s.cfg == nil || s.cfg.Security.OneTimeTokenTTL <= 0 {
			loginOutcome = iamTaxonomy.LoginOutcomeVerificationReq
			return nil, apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamTaxonomy.LoginOutcomeVerificationReq)
		}
		requestedAt := time.Now().UTC()
		verificationToken, _, issueErr := s.ott.Issue(ctx, "account_verify", user.ID.String())
		if issueErr != nil {
			loginOutcome = iamTaxonomy.OutcomeFailure
			return nil, fmt.Errorf("%w: failed to issue verification token: %v", iamTaxonomy.ErrAuthenticationUnavailable, issueErr)
		}
		requestID := uuid.Must(uuid.NewV7()).String()
		idempotencyKey := fmt.Sprintf("%s:%s:%s", "account_verify", user.ID.String(), requestID)
		streamMsg := infraredis.StreamMessage{
			Stream:         "mail:jobs",
			IdempotencyKey: idempotencyKey,
			Payload: map[string]string{
				"event_type":      "mail.verify_account.requested",
				"purpose":         "account_verify",
				"user_id":         user.ID.String(),
				"email":           user.Email,
				"fullname":        user.Fullname,
				"verify_token":    verificationToken,
				"requested_at":    requestedAt.Format(time.RFC3339Nano),
				"request_id":      requestID,
				"idempotency_key": idempotencyKey,
			},
		}
		// Publish fail -> fail login theo security policy, không fallback silent.
		publishStartedAt := time.Now()
		traceCtx, span := otel.Tracer("aurora-controlplane.iam").Start(ctx, "iam.login.publish_verify_mail_job")
		streamID, published, publishErr := s.streamPublisher.Publish(traceCtx, streamMsg, s.cfg.Security.OneTimeTokenTTL)
		iamMetrics.ObserveLoginVerifyMailPublish(published, publishErr, time.Since(publishStartedAt))
		span.SetAttributes(
			attribute.String("stream", streamMsg.Stream),
			attribute.String("event_type", streamMsg.Payload["event_type"]),
			attribute.String("purpose", streamMsg.Payload["purpose"]),
			attribute.String("user_id", streamMsg.Payload["user_id"]),
			attribute.String("idempotency_key", idempotencyKey),
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
		loginOutcome = iamTaxonomy.LoginOutcomeInvalidCredentials
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamTaxonomy.LoginOutcomeInvalidCredentials)
	}

	// Service dependency guard ở callsite để trả reason chính xác sớm.
	if s.registry == nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: cache registry is required", iamTaxonomy.ErrAuthenticationUnavailable)
	}
	if s.deviceRepo == nil {
		loginOutcome = iamTaxonomy.OutcomeFailure
		return nil, fmt.Errorf("%w: device repository is required", iamTaxonomy.ErrAuthenticationUnavailable)
	}

	now := time.Now().UTC()
	accessTTL := 15 * time.Minute
	refreshTTL := 168 * time.Hour
	if s.cfg != nil {
		if s.cfg.Security.AccessSecretTTL > 0 {
			accessTTL = s.cfg.Security.AccessSecretTTL
		}
		if s.cfg.Security.RefreshTokenTTL > 0 {
			refreshTTL = s.cfg.Security.RefreshTokenTTL
		}
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
	// UpsertLoginDevice là DB SoT để lấy tracked device persistent.
	trackedDevice, deviceErr := s.deviceRepo.UpsertLoginDevice(ctx, buildLoginDevice(user.ID, canonicalDevicePublicKey, req.IP, req.UserAgent, now, deviceName, clientDeviceID))
	iamMetrics.ObserveLoginLastSeenFlush(flushLabel)
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
	accessKey := uuid.NewString()
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
	accessExp := now.Add(accessTTL)
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
	refreshExp := now.Add(refreshTTL)

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

	if s.deviceRuntime != nil {
		// Runtime write lỗi: rollback theo callsite policy bằng revoke refresh token.
		// Không fallback chạy tiếp vì sẽ tạo access token không verify được runtime.
		runtimeTTL := accessTTL
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
		if setErr := s.deviceRuntime.SetDeviceRuntime(ctx, runtime, runtimeTTL); setErr != nil {
			if s.refreshTokenRepo != nil {
				_, _ = s.refreshTokenRepo.RevokeRefreshTokensByDeviceIDAndUserID(ctx, user.ID, trackedDeviceID)
			}
			loginOutcome = iamTaxonomy.OutcomeFailure
			return nil, fmt.Errorf("%w: failed to set device runtime: %v", iamTaxonomy.ErrAuthenticationUnavailable, setErr)
		}
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

func (s *AuthService) Logout(ctx context.Context, userID string, accessKey string) error {
	// Validate input ở callsite trước khi thao tác runtime/revoke.
	uid, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return fmt.Errorf("%w: %v", iamTaxonomy.ErrInvalidArgument, err)
	}
	accessKey = strings.TrimSpace(accessKey)
	var trackedDeviceRef string
	if s.deviceRuntime != nil && accessKey != "" {
		// Runtime read/delete là best-effort: lỗi cache không làm fail logout toàn bộ.
		record, getErr := s.deviceRuntime.GetDeviceRuntimeByUserDevice(ctx, userID, accessKey)
		if getErr == nil && record != nil {
			trackedDeviceRef = strings.TrimSpace(record.TrackedDeviceID)
		}
		_ = s.deviceRuntime.DeleteDeviceRuntimeByUserDevice(ctx, userID, accessKey)
	}
	if trackedDeviceRef != "" {
		// Có tracked device -> revoke scope theo device để chính xác hơn.
		if deviceUUID, parseErr := uuid.Parse(trackedDeviceRef); parseErr == nil {
			_, _ = s.refreshTokenRepo.RevokeRefreshTokensByDeviceIDAndUserID(ctx, uid, deviceUUID)
		}
	} else {
		// Fallback callsite: thiếu tracked ref thì revoke toàn user sessions.
		_, _ = s.refreshTokenRepo.RevokeRefreshTokensByUserID(ctx, uid, nil)
	}
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
			iamMetrics.ObserveDeviceCapLockSkip()
		} else if !ok {
			// Lock contention: worker khác đang xử lý cap flow cho user này.
			iamMetrics.ObserveDeviceCapLockSkip()
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
	iamMetrics.ObserveDeviceCapEvict("login_flow", len(evicted))
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
			iamMetrics.ObserveAuditPublish(event, "fallback_db")
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
		iamMetrics.ObserveAuditPublish(event, "fallback_db")
		return
	}
	iamMetrics.ObserveAuditPublish(event, "published")
}

func cleanString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
