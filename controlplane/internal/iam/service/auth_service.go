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
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	"controlplane/pkg/logger"

	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: Bỏ hằng số mặc định để ưu tiên nhận locale & timezone trực tiếp từ client

type AuthService struct {
	repo                  iamRepoInterface.AuthRepository
	refreshSvc            iamSvcInterface.SessionRefreshService
	deviceSvc             iamSvcInterface.DeviceSelfService // [COMMENT]: Sử dụng DeviceSelfService phục vụ quản trị thiết bị cá nhân
	registry              *cacheengine.CacheRegistry
	ott                   iamSvcInterface.OneTimeTokenService
	verificationPublisher iamSvcInterface.AccountVerificationPublisher
	billingOutboxNotifier iamSvcInterface.BillingOutboxNotifier
	acrClient             iamproto.SessionServiceClient
}

func NewAuthService(
	repo iamRepoInterface.AuthRepository,
	refreshSvc iamSvcInterface.SessionRefreshService,
	deviceSvc iamSvcInterface.DeviceSelfService,
	registry *cacheengine.CacheRegistry,
	ott iamSvcInterface.OneTimeTokenService,
	verificationPublisher iamSvcInterface.AccountVerificationPublisher,
	billingOutboxNotifier iamSvcInterface.BillingOutboxNotifier,
	acrClient iamproto.SessionServiceClient,
) iamSvcInterface.AuthService {
	return &AuthService{
		repo:                  repo,
		refreshSvc:            refreshSvc,
		deviceSvc:             deviceSvc,
		registry:              registry,
		ott:                   ott,
		verificationPublisher: verificationPublisher,
		billingOutboxNotifier: billingOutboxNotifier,
		acrClient:             acrClient,
	}
}

func (s *AuthService) RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) (err error) {
	result := iamMetrics.OutcomeSuccess
	defer func() {
		// Ghi nhận kết quả nghiệp vụ tổng thể của luồng đăng ký.
		iamMetrics.ServiceCall(ctx, result)
	}()

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
	// [COMMENT]: Lưu trực tiếp thông tin Locale và Timezone từ client gửi lên mà không áp đặt default locale cứng ở tầng domain service
	profile.CreatedAt = now
	profile.UpdatedAt = now

	// Thực hiện ghi dữ liệu xuống database và đo lường latency của transaction (I/O-bound).
	insertStart := time.Now()
	insertErr := s.repo.CreateRegisteredUser(ctx, user, profile)
	if insertErr != nil {
		// DB unique violation được map về domain duplicate; PostgreSQL unique index vẫn là SoT duy nhất.
		if errors.Is(insertErr, iamTaxonomy.ErrUserAlreadyExist) {

			result = iamMetrics.OutcomePreConditionFailed
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "CreateRegisteredUser", iamMetrics.OutcomePreConditionFailed, time.Since(insertStart), insertErr)

			return apperr.Wrap(iamTaxonomy.ErrUserAlreadyExist, insertErr, iamMetrics.OutcomePreConditionFailed)
		}
		result = iamMetrics.OutcomeFailureUnknown
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "CreateRegisteredUser", iamMetrics.OutcomeFailureUnknown, time.Since(insertStart), insertErr)
		return insertErr
	}

	// [COMMENT]: Mail xác minh là best-effort sau identity commit; resend khi login là recovery path nếu broker gián đoạn.
	publishCtx, publishCancel := context.WithTimeout(context.Background(), 2*time.Second)
	publishStart := time.Now()
	publishErr := s.publishAccountVerification(publishCtx, user.ID, user.Username, user.Email)
	publishCancel()
	if publishErr != nil {
		iamMetrics.Downstream(ctx, "broker", "PublishAccountVerification", iamMetrics.OutcomeFailureUnknown, time.Since(publishStart), publishErr)
		logger.SysError("iam.account_verification.publish", fmt.Sprintf("registration committed but verification message publish failed for user_id=%s: %v", user.ID, publishErr))
	} else {
		iamMetrics.Downstream(ctx, "broker", "PublishAccountVerification", iamMetrics.OutcomeSuccess, time.Since(publishStart), nil)
	}

	return nil
}

// [COMMENT]: VerifyAccount kiểm tra tính hợp lệ của mã kích hoạt (OTT) thông qua OneTimeTokenService,
// sau đó tiến hành kích hoạt tài khoản và gán role mặc định 'platform_user' cho người dùng.
func (s *AuthService) VerifyAccount(ctx context.Context, userID, eventID uuid.UUID, token string) error {
	// [COMMENT]: HTTP retry sau commit phải idempotent ngay cả khi Redis token đã được consume hoặc hết TTL.
	active, stateErr := s.repo.IsUserActive(ctx, userID)
	if stateErr != nil {
		return stateErr
	}
	if !active {
		// [COMMENT]: Validate trước nhưng chỉ consume sau DB commit; DB lỗi không được làm mất token retry.
		valid, err := s.ott.Validate(ctx, "account_verify", userID, eventID, token)
		if err != nil {
			// [COMMENT]: Request khác có thể commit + consume giữa IsUserActive và Validate.
			// Re-read DB; chỉ bỏ lỗi OTT khi durable activation đã thắng race.
			activeAfterRace, stateErr := s.repo.IsUserActive(ctx, userID)
			if stateErr != nil {
				return stateErr
			}
			if !activeAfterRace {
				return err
			}
			active = true
		}
		if !valid && !active {
			return iamTaxonomy.ErrTokenExpired
		}
	}

	// [COMMENT]: Event ID deterministic theo user giúp HTTP retry không sinh logical event thứ hai.
	billingEventID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("billing.wallet.personal.provision:"+userID.String()))
	event := &iamproto.PersonalWalletProvisionRequestedV1{
		EventId:       billingEventID[:],
		SchemaVersion: 1,
		OwnerId:       userID[:],
		OwnerType:     "PERSONAL",
		Currency:      "USD",
		OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("iam service: marshal account verified event: %w", err)
	}

	// [COMMENT]: Repository commit activation + role + domain event trong một PostgreSQL transaction.
	if err := s.repo.ActivateUser(ctx, userID, "platform_user", billingEventID, payload); err != nil {
		return err
	}
	if s.billingOutboxNotifier != nil {
		// [COMMENT]: Chỉ wake sau commit; channel đầy hoặc pod crash không làm mất event vì fallback vẫn quét durable outbox.
		s.billingOutboxNotifier.Notify()
	}
	// [COMMENT]: Concurrent verify có cùng deterministic event; chỉ một request xóa token, cả hai đều idempotent.
	if !active {
		// [COMMENT]: Consume sau commit chỉ là cleanup; Redis outage không được biến activation đã commit thành HTTP 500.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = s.ott.Consume(cleanupCtx, "account_verify", userID, eventID, token)
	}

	return nil
}

// [COMMENT]: publishAccountVerification phát fixed mail envelope; IAM không biết Zone, consumer hay template runtime.
func (s *AuthService) publishAccountVerification(ctx context.Context, userID uuid.UUID, username, email string) error {
	if s.ott == nil || s.verificationPublisher == nil {
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, nil, iamMetrics.OutcomeFailureUnknown)
	}

	eventID, eventErr := uuid.NewV7()
	if eventErr != nil {
		return fmt.Errorf("generate verification event ID: %w", eventErr)
	}
	verificationToken, expiresAt, issueErr := s.ott.Issue(ctx, "account_verify", userID, eventID)
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
// Phương thức này được gọi qua gRPC từ Gateway/ACR để CP đóng vai trò Data Plane (SoT).
func (s *AuthService) VerifyUserCredentials(ctx context.Context, req iamEntity.LoginRequest) (res *iamEntity.VerifyUserCredentialsResult, err error) {
	loginOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		iamMetrics.ServiceCall(ctx, loginOutcome)
	}()

	// [COMMENT]: 1. Truy xuất thông tin người dùng từ cơ sở dữ liệu (Single Source of Truth)
	// Nếu TenantDomain có giá trị (login qua username@tenant_domain), dùng query JOIN tenant_domains.
	// Nếu không có (login global), dùng query thường.
	now := time.Now()
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
		}
	} else {
		// [COMMENT]: Login global context — chỉ query bảng users
		user, loadErr = s.repo.LoginUserGlobal(ctx, req.Username)
	}
	if loadErr != nil {
		if errors.Is(loadErr, iamTaxonomy.ErrUserNotFound) || errors.Is(loadErr, iamTaxonomy.ErrRoleRequired) || errors.Is(loadErr, iamTaxonomy.ErrInvalidCredentials) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "LoginUserByUsername", iamMetrics.OutcomeInvalidCredential, time.Since(now), loadErr)
			return nil, apperr.Wrap(loadErr, loadErr, iamMetrics.OutcomeInvalidCredential)
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "LoginUserByUsername", iamMetrics.OutcomeFailureUnknown, time.Since(now), loadErr)
		return nil, fmt.Errorf("%w: failed to get login user: %v", iamTaxonomy.ErrAuthenticationUnavailable, loadErr)
	}

	// [COMMENT]: 2. Xác thực mật khẩu sử dụng thư viện băm bảo mật
	if user.PasswordHash == nil {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}
	verified, verifyErr := security.VerifyPassword(*user.PasswordHash, req.Password)
	if verifyErr != nil {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, verifyErr, iamMetrics.OutcomeInvalidCredential)
	}
	if !verified {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}

	// [COMMENT]: 3. Kiểm tra trạng thái tài khoản của người dùng
	switch user.Status {
	case iamEntity.UserStatusPendingActive:
		// [COMMENT]: Password đã đúng; nhánh pending tự sở hữu cooldown và direct broker resend.
		if s.verificationPublisher == nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, nil, iamMetrics.OutcomeFailureUnknown)
		}
		cooldownKey := "iam:account_verify:resend_cooldown:" + user.ID.String()
		acquired, cooldownErr := s.registry.L2.Client().SetNX(ctx, cooldownKey, "1", time.Minute).Result()
		if cooldownErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, cooldownErr, "cache_unavailable")
		}
		if acquired {
			publishStart := time.Now()
			publishErr := s.publishAccountVerification(ctx, user.ID, user.Username, user.Email)
			if publishErr != nil {
				// [COMMENT]: Publish chưa thành công thì nhả cooldown best-effort để lần login sau có thể recovery ngay.
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				_ = s.registry.L2.Client().Del(cleanupCtx, cooldownKey).Err()
				cleanupCancel()
				iamMetrics.Downstream(ctx, "broker", "PublishAccountVerification", iamMetrics.OutcomeFailureUnknown, time.Since(publishStart), publishErr)
				loginOutcome = iamMetrics.OutcomeFailureUnknown
				return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, publishErr, iamMetrics.OutcomeFailureUnknown)
			}
			iamMetrics.Downstream(ctx, "broker", "PublishAccountVerification", iamMetrics.OutcomeSuccess, time.Since(publishStart), nil)
		}

		// [COMMENT]: Cooldown winner hay loser đều trả cùng taxonomy; pending account không được cấp session.
		loginOutcome = iamMetrics.OutcomePreConditionFailed
		return nil, apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamMetrics.OutcomePreConditionFailed)

	case iamEntity.UserStatusSuspended, iamEntity.UserStatusDisabled:
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)

	case iamEntity.UserStatusActive:
		// [COMMENT]: Tài khoản đang hoạt động bình thường, cho phép tiếp tục đăng nhập

	default:
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}

	// [COMMENT]: Chỉ canonical Ed25519 key hợp lệ mới được bind vào session; empty/garbage key phải fail trước khi ghi device.
	canonicalPublicKey, keyErr := normalizeUserDevicePublicKey(req.DevicePublicKey)
	if keyErr != nil {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, keyErr, iamMetrics.OutcomeInvalidCredential)
	}
	req.DevicePublicKey = canonicalPublicKey

	// [COMMENT]: 4. Phân giải/Tìm kiếm thiết bị đang hoạt động tương thích
	matchedClientDeviceID, err := s.deviceSvc.ResolveDeviceIDByKey(ctx, user.ID, req.DevicePublicKey)

	var clientDeviceID string
	if err != nil || matchedClientDeviceID == "" {
		newDeviceID := uuid.New()
		clientDeviceID = newDeviceID.String()
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
		PublicKeyFingerprint: fingerprint,
		ClientDeviceID:       cleanOptionalString(&clientDeviceID),
		LastSeenIP:           cleanOptionalString(&req.RemoteIP),
		LastSeenUserAgent:    cleanOptionalString(&req.UserAgent),
		UpdatedAt:            now.UTC(),
	}

	// [COMMENT]: Đăng ký/Cập nhật thiết bị vào cơ sở dữ liệu
	trackedDevice, deviceErr := s.deviceSvc.RegisterLoginDevice(ctx, loginDevice)
	if deviceErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to upsert login device: %v", iamTaxonomy.ErrAuthenticationUnavailable, deviceErr)
	}
	if trackedDevice == nil || strings.TrimSpace(trackedDevice.ID) == "" || trackedDevice.RevokedAt != nil {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidCredentials, nil, iamMetrics.OutcomeInvalidCredential)
	}
	trackedDeviceID, trackedErr := uuid.Parse(strings.TrimSpace(trackedDevice.ID))
	if trackedErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, fmt.Errorf("%w: failed to parse tracked device ID: %v", iamTaxonomy.ErrAuthenticationUnavailable, trackedErr)
	}

	// [COMMENT]: 5. Sinh Refresh Token nếu thiết bị được đánh dấu tin cậy (Trust Device)
	var rawRefresh string
	var refreshExpiresAt time.Time
	if req.TrustDevice {
		var tenantUUIDPtr *uuid.UUID
		if tenantID != "" {
			parsed, err := uuid.Parse(tenantID)
			if err != nil {
				loginOutcome = iamMetrics.OutcomeFailureUnknown
				return nil, fmt.Errorf("failed to parse tenant ID: %w", err)
			}
			tenantUUIDPtr = &parsed
		}

		var refreshErr error
		rawRefresh, refreshExpiresAt, refreshErr = s.refreshSvc.CreateRefreshToken(ctx, user.ID, trackedDeviceID, tenantUUIDPtr)
		if refreshErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return nil, refreshErr
		}
	}

	return &iamEntity.VerifyUserCredentialsResult{
		Valid:  true,
		UserID: user.ID.String(),
		// [COMMENT]: RoleID là UUID của role đang hoạt động, ACR sẽ inject vào JWT claims và forward qua header X-User-Role-ID
		RoleID:                user.RoleID,
		Level:                 user.Level,
		TenantID:              tenantID,
		ClientDeviceID:        clientDeviceID,
		RefreshToken:          rawRefresh,
		RefreshTokenExpiresAt: refreshExpiresAt,
		Username:              user.Username,
		// [COMMENT]: Trả key canonical từ row đã persist thay vì echo trực tiếp input không đáng tin từ client.
		ClientProofPublicKey: trackedDevice.PublicKey,
		// [COMMENT]: TenantCode chỉ có giá trị khi login qua tenant_domain. Rỗng nếu login global.
		TenantCode: tenantCode,
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
	identity, user, err := s.repo.VerifyExternalIdentity(ctx, req)
	if err != nil {
		return nil, err
	}
	if identity == nil || user == nil || user.Status != iamEntity.UserStatusActive {
		return nil, iamTaxonomy.ErrInvalidCredentials
	}
	if user.PasswordHash == nil || strings.TrimSpace(*user.PasswordHash) == "" {
		return nil, iamTaxonomy.ErrVerificationRequired
	}

	matchedClientDeviceID, _ := s.deviceSvc.ResolveDeviceIDByKey(ctx, user.ID, req.DevicePublicKey)
	clientDeviceID := req.ClientDeviceID
	if matchedClientDeviceID != "" {
		if parsed, parseErr := uuid.Parse(matchedClientDeviceID); parseErr == nil {
			clientDeviceID = parsed
		}
	}
	if clientDeviceID == uuid.Nil {
		clientDeviceID = uuid.New()
	}

	deviceName := strings.TrimSpace(req.DeviceName)
	if deviceName == "" {
		deviceName = "OAuth browser"
	}
	deviceType := strings.TrimSpace(req.DeviceType)
	if deviceType == "" {
		deviceType = "browser"
	}
	fp := sha256.Sum256([]byte(req.DevicePublicKey))
	clientDeviceIDValue := clientDeviceID.String()
	remoteIP := req.RemoteIP
	userAgent := req.UserAgent
	loginDevice := iamEntity.Device{
		UserID:               user.ID,
		DeviceName:           deviceName,
		DeviceType:           &deviceType,
		PublicKey:            req.DevicePublicKey,
		PublicKeyFingerprint: hex.EncodeToString(fp[:]),
		ClientDeviceID:       cleanOptionalString(&clientDeviceIDValue),
		LastSeenIP:           cleanOptionalString(&remoteIP),
		LastSeenUserAgent:    cleanOptionalString(&userAgent),
		UpdatedAt:            time.Now().UTC(),
	}
	trackedDevice, err := s.deviceSvc.RegisterLoginDevice(ctx, loginDevice)
	if err != nil {
		return nil, fmt.Errorf("%w: register external login device: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	if trackedDevice == nil || trackedDevice.RevokedAt != nil || strings.TrimSpace(trackedDevice.ID) == "" {
		return nil, iamTaxonomy.ErrInvalidCredentials
	}
	trackedDeviceID, err := uuid.Parse(trackedDevice.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: parse external login device: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}

	var rawRefresh string
	var refreshExpiresAt time.Time
	if req.TrustDevice {
		rawRefresh, refreshExpiresAt, err = s.refreshSvc.CreateRefreshToken(ctx, user.ID, trackedDeviceID, nil)
		if err != nil {
			return nil, err
		}
	}
	if s.billingOutboxNotifier != nil {
		s.billingOutboxNotifier.Notify()
	}

	return &iamEntity.ExternalLoginResult{
		Valid:                 true,
		UserID:                user.ID.String(),
		RoleID:                user.RoleID,
		Level:                 user.Level,
		ClientDeviceID:        clientDeviceID.String(),
		RefreshToken:          rawRefresh,
		RefreshTokenExpiresAt: refreshExpiresAt,
		Username:              user.Username,
		ClientProofPublicKey:  trackedDevice.PublicKey,
		ZoneCode:              req.ZoneCode,
	}, nil
}
