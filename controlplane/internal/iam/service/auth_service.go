package iamSvcImpl

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	iamproto "controlplane/internal/iam/transport/proto"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: AuthService thực thi các nghiệp vụ xác thực đăng nhập, đăng ký, MFA và phục hồi tài khoản
type AuthService struct {
	repo                  iamRepoInterface.AuthRepository
	sessionRefreshSvc     iamSvcInterface.SessionRefreshService
	selfDeviceSvc         iamSvcInterface.SelfDeviceService
	cacheEngine           *cacheengine.CacheRegistry
	oneTimeTokenSvc       iamSvcInterface.OneTimeTokenService
	verificationPublisher iamSvcInterface.AccountVerificationPublisher
	lifecycleFactNotifier iamSvcInterface.LifecycleFactNotifier
	mfaSvc                iamSvcInterface.MfaService
	metrics               observability.WorkflowRecorder
}

// [COMMENT]: NewAuthService khởi tạo thể hiện mới của AuthService
func NewAuthService(
	repo iamRepoInterface.AuthRepository,
	sessionRefreshSvc iamSvcInterface.SessionRefreshService,
	selfDeviceSvc iamSvcInterface.SelfDeviceService,
	cacheEngine *cacheengine.CacheRegistry,
	oneTimeTokenSvc iamSvcInterface.OneTimeTokenService,
	verificationPublisher iamSvcInterface.AccountVerificationPublisher,
	lifecycleFactNotifier iamSvcInterface.LifecycleFactNotifier,
	mfaSvc iamSvcInterface.MfaService,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.AuthService {
	return &AuthService{
		repo:                  repo,
		sessionRefreshSvc:     sessionRefreshSvc,
		selfDeviceSvc:         selfDeviceSvc,
		cacheEngine:           cacheEngine,
		oneTimeTokenSvc:       oneTimeTokenSvc,
		verificationPublisher: verificationPublisher,
		lifecycleFactNotifier: lifecycleFactNotifier,
		mfaSvc:                mfaSvc,
		metrics:               metrics,
	}
}

func (s *AuthService) RegisterAccount(ctx context.Context, account *iamEntity.RegisterAccount) (registration *iamEntity.RegisterAccountResult, err error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// Đo lường thời gian băm mật khẩu để SRE theo dõi mức sử dụng CPU (CPU-bound).
	passwordHash, hashErr := security.HashPassword(account.Password)
	if hashErr != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, hashErr, "internal")
	}

	now := time.Now().UTC()
	userID, idErr := uuid.NewV7()
	if idErr != nil {
		return nil, fmt.Errorf("%w: failed to generate user ID: %v", iamTaxonomy.ErrAuthenticationUnavailable, idErr)
	}

	account.ID = userID
	account.PasswordHash = passwordHash
	account.Status = iamEntity.UserStatusPendingActive
	account.CreatedAt = now
	account.UpdatedAt = now

	// Thực hiện ghi dữ liệu xuống database và đo lường latency của transaction (I/O-bound).
	insertErr := s.repo.CreateRegisteredUser(ctx, account)
	if insertErr != nil {
		// DB unique violation được map về domain duplicate; PostgreSQL unique index vẫn là SoT duy nhất.
		if errors.Is(insertErr, iamTaxonomy.ErrUserAlreadyExist) {
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
			return nil, apperr.Wrap(iamTaxonomy.ErrUserAlreadyExist, insertErr, "already_exists")
		}
		return nil, insertErr
	}

	// [COMMENT]: Mail xác minh là side effect phục hồi được sau identity commit;
	// pending login sẽ phát lại dưới cooldown nếu OTT hoặc broker tạm thời lỗi.
	publishCtx, publishCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	publishErr := s.publishAccountVerification(publishCtx, account.ID, account.Username, account.Email)
	publishCancel()
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return &iamEntity.RegisterAccountResult{VerificationDispatched: publishErr == nil}, nil
}

// [COMMENT]: VerifyAccount kiểm tra tính hợp lệ của mã kích hoạt thông qua OneTimeTokenService trong Security Redis,
// sau đó tiến hành kích hoạt tài khoản, cấp role mặc định và seed personal workspaces cho tất cả active zones.
func (s *AuthService) VerifyAccount(ctx context.Context, userID, eventID uuid.UUID, token string) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: 1. Redis Gatekeeper: Xác thực token trong Security Redis.
	// Không xóa token sau khi xác thực để token tự hết hạn theo TTL (15 phút), hỗ trợ retry idempotent an toàn.
	valid, err := s.oneTimeTokenSvc.Validate(ctx, "account_verify", userID, eventID, token)
	if err != nil {
		return err
	}
	if !valid {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return iamTaxonomy.ErrTokenExpired
	}

	// [COMMENT]: 2. IAM khởi tạo command tạo ví cá nhân sang Cost Manager
	lifecycleEventID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("billing.personal_wallet.provision.requested:"+userID.String()))
	event := &iamproto.PersonalWalletProvisionRequestedV1{
		EventId:       lifecycleEventID[:],
		SchemaVersion: 1,
		OwnerId:       userID[:],
		OwnerType:     "PERSONAL",
		Currency:      "USD",
		OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("iam service: marshal personal wallet provision command: %w", err)
	}

	now := time.Now().UTC()
	activation := iamEntity.AccountActivation{
		UserID:                userID,
		RoleCode:              "platform_user",
		LifecycleEventID:      lifecycleEventID,
		LifecycleEventPayload: payload,
	}
	bootstrapWorkspaces := iamEntity.BootstrapPersonalWorkspaces{
		OwnerID:     userID,
		Name:        "Personal",
		CodePrefix:  "personal",
		Description: "Default personal workspace created during account activation.",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// [COMMENT]: 3. Repository commit kích hoạt user, gán role platform, seed workspace cá nhân
	// trên từng active Zone và outbox command tạo ví trong 1 PostgreSQL transaction duy nhất (CTE nguyên tử).
	if err := s.repo.ActivateUser(ctx, activation, bootstrapWorkspaces); err != nil {
		return err
	}

	// [COMMENT]: 4. Đánh thức worker phát Outbox ngầm bất đồng bộ (Non-blocking signal)
	s.lifecycleFactNotifier.Notify()

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}

// [COMMENT]: publishAccountVerification phát fixed mail envelope; IAM không biết Zone, consumer hay template runtime.
func (s *AuthService) publishAccountVerification(ctx context.Context, userID uuid.UUID, username, email string) error {
	eventID, eventErr := uuid.NewV7()
	if eventErr != nil {
		return fmt.Errorf("generate verification event ID: %w", eventErr)
	}
	verificationToken, expiresAt, issueErr := s.oneTimeTokenSvc.Issue(ctx, "account_verify", userID, eventID)
	if issueErr != nil {
		return fmt.Errorf("issue verification token: %w", issueErr)
	}

	// [COMMENT]: Service chỉ tạo parameter nghiệp vụ; transport adapter tự encode Protobuf và chọn Kafka topic.
	dispatch := iamEntity.AccountVerificationDispatch{
		EventID:   eventID,
		Recipient: email,
		Parameter: map[string]string{
			"username":     username,
			"user_id":      userID.String(),
			"event_id":     eventID.String(),
			"verify_token": verificationToken,
		},
		ExpiresAt: expiresAt,
	}
	if publishErr := s.verificationPublisher.PublishAccountVerification(ctx, dispatch); publishErr != nil {
		return fmt.Errorf("publish verification message: %w", publishErr)
	}

	return nil
}

// [COMMENT]: VerifyUserCredentials thực hiện xác thực thông tin đăng nhập (username, password),
// kiểm tra trạng thái tài khoản, định danh/upsert thiết bị và sinh Opaque Refresh Token (nếu được yêu cầu).
// Phương thức này được gọi qua Shared L2 Redis Pub/Sub (iam.auth.verify_credentials) từ ACR để CP đóng vai trò Data Plane (SoT).
func (s *AuthService) VerifyUserCredentials(ctx context.Context, req iamEntity.LoginRequest) (res *iamEntity.VerifyUserCredentialsResult, err error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: 1. Truy xuất thông tin người dùng từ cơ sở dữ liệu (Single Source of Truth)
	// Nếu TenantDomain có giá trị (login qua username@tenant_domain), dùng query JOIN tenant_domains.
	// Nếu không có (login global), dùng query thường.
	var (
		user       *iamEntity.LoginUser
		loadErr    error
		tenantID   string
		tenantCode string
	)
	if req.TenantDomain != "" {
		// [COMMENT]: Login tenant context — JOIN tenant_memberships + tenant_domains
		user, loadErr = s.repo.LoginUserTenant(ctx, req.Username, req.TenantDomain)
		if user != nil {
			if user.TenantID != nil {
				tenantID = *user.TenantID
			}
			if user.TenantCode != nil {
				tenantCode = *user.TenantCode
			}
		}
	} else {
		// [COMMENT]: Login global context — chỉ query bảng users
		user, loadErr = s.repo.LoginUserGlobal(ctx, req.Username)
	}
	if loadErr != nil {
		if errors.Is(loadErr, iamTaxonomy.ErrUserNotFound) || errors.Is(loadErr, iamTaxonomy.ErrRoleRequired) || errors.Is(loadErr, iamTaxonomy.ErrInvalidCredentials) {
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
			return nil, apperr.Wrap(loadErr, loadErr, "unauthenticated")
		}
		return nil, fmt.Errorf("%w: failed to get login user: %v", iamTaxonomy.ErrAuthenticationUnavailable, loadErr)
	}

	// [COMMENT]: 2. Xác thực mật khẩu sử dụng thư viện băm bảo mật
	verified, verifyErr := security.VerifyPassword(user.PasswordHash, req.Password)
	if verifyErr != nil || !verified {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, verifyErr, "unauthenticated")
	}

	// [COMMENT]: 3. Kiểm tra trạng thái tài khoản của người dùng
	switch user.Status {
	case iamEntity.UserStatusPendingActive:
		// [COMMENT]: Password đã đúng; nhánh pending tự sở hữu cooldown và direct broker resend.
		cooldownKey := "iam:account_verify:resend_cooldown:" + user.ID.String()
		acquired, cooldownErr := s.cacheEngine.L2.Client().SetNX(ctx, cooldownKey, "1", time.Minute).Result()
		if cooldownErr != nil {
			return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, cooldownErr, "cache_unavailable")
		}
		if acquired {
			publishErr := s.publishAccountVerification(ctx, user.ID, user.Username, user.Email)
			if publishErr != nil {
				// [COMMENT]: Publish chưa thành công thì nhả cooldown best-effort để lần login sau có thể recovery ngay.
				cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
				_ = s.cacheEngine.L2.Client().Del(cleanupCtx, cooldownKey).Err()
				cleanupCancel()
				return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, publishErr, "unavailable")
			}
		}

		// [COMMENT]: Cooldown winner hay loser đều trả cùng taxonomy; pending account không được cấp session.
		result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		return nil, apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, "precondition_failed")

	case iamEntity.UserStatusSuspended, iamEntity.UserStatusDisabled:
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, "unauthenticated")

	case iamEntity.UserStatusActive:
		// [COMMENT]: Tài khoản đang hoạt động bình thường, cho phép tiếp tục đăng nhập

	default:
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, "unauthenticated")
	}

	// [COMMENT]: Chỉ canonical Ed25519 key hợp lệ mới được bind vào session; empty/garbage key phải fail trước khi ghi device.
	canonicalPublicKey, keyErr := normalizeUserDevicePublicKey(req.DevicePublicKey)
	if keyErr != nil {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, keyErr, "unauthenticated")
	}
	req.DevicePublicKey = canonicalPublicKey

	mfaSetting, mfaErr := s.mfaSvc.GetLoginSetting(ctx, user.ID)
	if mfaErr != nil {
		return nil, fmt.Errorf("%w: check mfa state: %v", iamTaxonomy.ErrAuthenticationUnavailable, mfaErr)
	}
	if mfaSetting != nil {
		// [COMMENT]: Primary credentials are valid, but no device/refresh
		// side effect is allowed until the ACR MFA challenge is completed.
		result, reason = observability.ResultSuccess, observability.ReasonNone
		return &iamEntity.VerifyUserCredentialsResult{
			Valid:                true,
			MFARequired:          true,
			UserID:               user.ID.String(),
			MFASettingID:         mfaSetting.ID.String(),
			Level:                user.Level,
			TenantID:             tenantID,
			ClientDeviceID:       req.ClientDeviceID.String(),
			Username:             user.Username,
			ClientProofPublicKey: canonicalPublicKey,
			TenantCode:           tenantCode,
		}, nil
	}

	// [COMMENT]: 4. Đăng ký / Cập nhật thiết bị vào cơ sở dữ liệu (Atomic CTE Upsert)
	var clientDeviceIDPtr *uuid.UUID
	if req.ClientDeviceID != uuid.Nil {
		clientDeviceIDPtr = &req.ClientDeviceID
	}

	loginDevice := iamEntity.Device{
		UserID:            user.ID,
		DeviceName:        req.DeviceName,
		DeviceType:        req.DeviceType,
		PublicKey:         req.DevicePublicKey,
		ClientDeviceID:    clientDeviceIDPtr,
		LastSeenIP:        cleanOptionalString(&req.RemoteIP),
		LastSeenUserAgent: cleanOptionalString(&req.UserAgent),
	}

	trackedDevice, deviceErr := s.selfDeviceSvc.RegisterLoginDevice(ctx, loginDevice)
	if deviceErr != nil {
		return nil, fmt.Errorf("%w: failed to upsert login device: %v", iamTaxonomy.ErrAuthenticationUnavailable, deviceErr)
	}
	if trackedDevice == nil || trackedDevice.ID == uuid.Nil || trackedDevice.RevokedAt != nil {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, "unauthenticated")
	}
	trackedDeviceID := trackedDevice.ID
	clientDeviceID := uuid.Nil
	if trackedDevice.ClientDeviceID != nil {
		clientDeviceID = *trackedDevice.ClientDeviceID
	}

	// [COMMENT]: 5. Sinh Refresh Token nếu thiết bị được đánh dấu tin cậy (Trust Device)
	var rawRefresh string
	var refreshExpiresAt time.Time
	if req.TrustDevice {
		var refreshErr error
		rawRefresh, refreshExpiresAt, refreshErr = s.sessionRefreshSvc.IssueDeviceRefreshToken(ctx, user.ID, trackedDeviceID)
		if refreshErr != nil {
			return nil, refreshErr
		}
	}

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return &iamEntity.VerifyUserCredentialsResult{
		Valid:                 true,
		UserID:                user.ID.String(),
		Level:                 user.Level,
		TenantID:              tenantID,
		ClientDeviceID:        clientDeviceID.String(),
		RefreshToken:          rawRefresh,
		RefreshTokenExpiresAt: refreshExpiresAt,
		Username:              user.Username,
		// [COMMENT]: Trả key canonical từ row đã persist thay vì echo trực tiếp input không đáng tin từ client.
		ClientProofPublicKey: trackedDevice.PublicKey,
		// [COMMENT]: TenantCode chỉ có giá trị khi login qua tenant_domain. Rỗng nếu login global.
		TenantCode: tenantCode,
	}, nil
}

func (s *AuthService) VerifyMfaLogin(
	ctx context.Context,
	req iamEntity.MFALoginRequest,
) (*iamEntity.VerifyUserCredentialsResult, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	if req.UserID == uuid.Nil {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, iamTaxonomy.ErrMFAChallengeInvalid
	}
	if req.MFASettingID == uuid.Nil {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, iamTaxonomy.ErrMFAChallengeInvalid
	}
	var (
		user       *iamEntity.LoginUser
		loadErr    error
		tenantID   string
		tenantCode string
	)
	if strings.TrimSpace(req.TenantDomain) != "" {
		user, loadErr = s.repo.LoginUserTenant(ctx, req.Username, req.TenantDomain)
		if user != nil {
			if user.TenantID != nil {
				tenantID = *user.TenantID
			}
			if user.TenantCode != nil {
				tenantCode = *user.TenantCode
			}
		}
	} else {
		user, loadErr = s.repo.LoginUserGlobal(ctx, req.Username)
	}
	if loadErr != nil || user == nil || user.ID != req.UserID || user.Status != iamEntity.UserStatusActive {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, iamTaxonomy.ErrMFAChallengeInvalid
	}
	if err := s.mfaSvc.VerifyLogin(ctx, req.UserID, req.MFASettingID, req.Method, req.Code); err != nil {
		if errors.Is(err, iamTaxonomy.ErrMFAChallengeInvalid) || errors.Is(err, iamTaxonomy.ErrMFAInvalidCode) {
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		}
		return nil, err
	}

	canonicalPublicKey, err := normalizeUserDevicePublicKey(req.DevicePublicKey)
	if err != nil {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, iamTaxonomy.ErrMFAChallengeInvalid
	}
	var clientDeviceIDPtr *uuid.UUID
	if req.ClientDeviceID != uuid.Nil {
		clientDeviceIDPtr = &req.ClientDeviceID
	}

	loginDevice := iamEntity.Device{
		UserID:            user.ID,
		DeviceName:        req.DeviceName,
		DeviceType:        req.DeviceType,
		PublicKey:         canonicalPublicKey,
		ClientDeviceID:    clientDeviceIDPtr,
		LastSeenIP:        cleanOptionalString(&req.RemoteIP),
		LastSeenUserAgent: cleanOptionalString(&req.UserAgent),
	}
	trackedDevice, err := s.selfDeviceSvc.RegisterLoginDevice(ctx, loginDevice)
	if err != nil || trackedDevice == nil || trackedDevice.ID == uuid.Nil || trackedDevice.RevokedAt != nil {
		return nil, fmt.Errorf("%w: register mfa login device: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	trackedDeviceID := trackedDevice.ID
	clientDeviceID := uuid.Nil
	if trackedDevice.ClientDeviceID != nil {
		clientDeviceID = *trackedDevice.ClientDeviceID
	}

	var rawRefresh string
	var refreshExpiresAt time.Time
	if req.TrustDevice {
		rawRefresh, refreshExpiresAt, err = s.sessionRefreshSvc.IssueDeviceRefreshToken(ctx, user.ID, trackedDeviceID)
		if err != nil {
			return nil, err
		}
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return &iamEntity.VerifyUserCredentialsResult{
		Valid:                 true,
		UserID:                user.ID.String(),
		Level:                 user.Level,
		TenantID:              tenantID,
		ClientDeviceID:        clientDeviceID.String(),
		RefreshToken:          rawRefresh,
		RefreshTokenExpiresAt: refreshExpiresAt,
		Username:              user.Username,
		ClientProofPublicKey:  trackedDevice.PublicKey,
		TenantCode:            tenantCode,
	}, nil
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

// VerifyExternalIdentity consumes only the canonical identity produced by ACR.
// Provider parsing and cryptographic assertion verification must stay upstream.
func (s *AuthService) VerifyExternalIdentity(
	ctx context.Context,
	req iamEntity.ExternalLoginRequest,
) (*iamEntity.ExternalLoginResult, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	identity, user, err := s.repo.VerifyExternalIdentity(ctx, req)
	if err != nil {
		return nil, err
	}
	if identity == nil || user == nil || user.Status != iamEntity.UserStatusActive {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, iamTaxonomy.ErrInvalidCredentials
	}
	if strings.TrimSpace(user.PasswordHash) == "" {
		// OAuth is only another credential for an existing password-backed
		// account; the callback must never open an onboarding path.
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, iamTaxonomy.ErrInvalidCredentials
	}
	mfaSetting, mfaErr := s.mfaSvc.GetLoginSetting(ctx, user.ID)
	if mfaErr != nil {
		return nil, fmt.Errorf("%w: check external login mfa state: %v", iamTaxonomy.ErrAuthenticationUnavailable, mfaErr)
	}
	if mfaSetting != nil {
		result, reason = observability.ResultSuccess, observability.ReasonNone
		return &iamEntity.ExternalLoginResult{
			Valid:        true,
			MFARequired:  true,
			UserID:       user.ID.String(),
			MFASettingID: mfaSetting.ID.String(),
			Level:        user.Level,
			Username:     user.Username,
			ZoneCode:     req.ZoneCode,
		}, nil
	}

	var clientDeviceIDPtr *uuid.UUID
	if req.ClientDeviceID != uuid.Nil {
		clientDeviceIDPtr = &req.ClientDeviceID
	}
	loginDevice := iamEntity.Device{
		UserID:            user.ID,
		DeviceName:        req.DeviceName,
		DeviceType:        req.DeviceType,
		PublicKey:         req.DevicePublicKey,
		ClientDeviceID:    clientDeviceIDPtr,
		LastSeenIP:        cleanOptionalString(&req.RemoteIP),
		LastSeenUserAgent: cleanOptionalString(&req.UserAgent),
	}
	trackedDevice, err := s.selfDeviceSvc.RegisterLoginDevice(ctx, loginDevice)
	if err != nil {
		return nil, fmt.Errorf("%w: register external login device: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	if trackedDevice == nil || trackedDevice.RevokedAt != nil || trackedDevice.ID == uuid.Nil {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, iamTaxonomy.ErrInvalidCredentials
	}
	trackedDeviceID := trackedDevice.ID
	clientDeviceID := uuid.Nil
	if trackedDevice.ClientDeviceID != nil {
		clientDeviceID = *trackedDevice.ClientDeviceID
	}

	var rawRefresh string
	var refreshExpiresAt time.Time
	if req.TrustDevice {
		rawRefresh, refreshExpiresAt, err = s.sessionRefreshSvc.IssueDeviceRefreshToken(ctx, user.ID, trackedDeviceID)
		if err != nil {
			return nil, err
		}
	}
	s.lifecycleFactNotifier.Notify()

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return &iamEntity.ExternalLoginResult{
		Valid:                 true,
		UserID:                user.ID.String(),
		Level:                 user.Level,
		ClientDeviceID:        clientDeviceID.String(),
		RefreshToken:          rawRefresh,
		RefreshTokenExpiresAt: refreshExpiresAt,
		Username:              user.Username,
		ClientProofPublicKey:  trackedDevice.PublicKey,
		ZoneCode:              req.ZoneCode,
	}, nil
}
