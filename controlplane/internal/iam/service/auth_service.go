package iamSvcImpl

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"sync"
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

	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: Bỏ hằng số mặc định để ưu tiên nhận locale & timezone trực tiếp từ client

type AuthService struct {
	repo       iamRepoInterface.AuthRepository
	refreshSvc iamSvcInterface.SessionRefreshService
	deviceSvc  iamSvcInterface.DeviceSelfService // [COMMENT]: Sử dụng DeviceSelfService phục vụ quản trị thiết bị cá nhân
	registry   *cacheengine.CacheRegistry
	ott        iamSvcInterface.OneTimeTokenService
	outboxRepo iamRepoInterface.IamOutboxRepository
	cfg        *config.Config
	acrClient  iamproto.SessionServiceClient
	wg         sync.WaitGroup // [COMMENT]: WaitGroup theo dõi và đợi các tác vụ nền (background updates) kết thúc
}

func NewAuthService(cfg *config.Config,
	repo iamRepoInterface.AuthRepository,
	refreshSvc iamSvcInterface.SessionRefreshService,
	deviceSvc iamSvcInterface.DeviceSelfService,
	registry *cacheengine.CacheRegistry,
	ott iamSvcInterface.OneTimeTokenService,
	outboxRepo iamRepoInterface.IamOutboxRepository,
	acrClient iamproto.SessionServiceClient,
) iamSvcInterface.AuthService {
	return &AuthService{
		repo:       repo,
		refreshSvc: refreshSvc,
		deviceSvc:  deviceSvc,
		registry:   registry,
		ott:        ott,
		outboxRepo: outboxRepo,
		cfg:        cfg,
		acrClient:  acrClient,
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
		// DB unique violation được map về domain duplicate ở repo; sau đó mark presence
		// best-effort để các request sau short-circuit sớm.
		if errors.Is(insertErr, iamTaxonomy.ErrUserAlreadyExist) {

			result = iamMetrics.OutcomePreConditionFailed
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "CreateRegisteredUser", iamMetrics.OutcomePreConditionFailed, time.Since(insertStart), insertErr)

			// [COMMENT]: Chạy cập nhật presence bitmap bất đồng bộ cho user đã trùng trong DB.
			// Sử dụng context.WithoutCancel để tránh việc goroutine bị dừng đột ngột khi request context cha bị hủy.
			s.wg.Add(1)
			go func(bgCtx context.Context, username, email string) {
				defer s.wg.Done()
				defer func() {
					if r := recover(); r != nil {
						// [COMMENT]: Ghi nhận log/panic tránh làm sập tiến trình Control Plane chính
					}
				}()

				rdb := s.registry.L2.Client()
				// [COMMENT]: Ghi nhận presence của user bị trùng vào Redis Bitmap
				if usernameDigest, digestErr := security.PresenceHMACSHA256Hex("iam.register.username", username); digestErr == nil {
					if emailDigest, digestErr := security.PresenceHMACSHA256Hex("iam.register.email", email); digestErr == nil {
						pipe := rdb.Pipeline()
						pipe.SetBit(bgCtx, "iam:register:bitmap:username", computeBitmapIndex(usernameDigest), 1)
						pipe.SetBit(bgCtx, "iam:register:bitmap:email", computeBitmapIndex(emailDigest), 1)
						_, _ = pipe.Exec(bgCtx)
					}
				}
			}(context.WithoutCancel(ctx), user.Username, user.Email)

			return apperr.Wrap(iamTaxonomy.ErrUserAlreadyExist, insertErr, iamMetrics.OutcomePreConditionFailed)
		}
		result = iamMetrics.OutcomeFailureUnknown
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "CreateRegisteredUser", iamMetrics.OutcomeFailureUnknown, time.Since(insertStart), insertErr)
		return insertErr
	}

	// [COMMENT]: Đẩy outbox job gửi mail kích hoạt tài khoản ngay sau khi đăng ký thành công (best-effort)
	// Nếu có lỗi ghi outbox ở đây, ta chỉ ghi nhận log cảnh báo chứ không fail toàn bộ luồng đăng ký vì người dùng có thể kích hoạt lại khi đăng nhập.
	if mailErr := s.pushVerifyAccountOutboxJob(ctx, user.ID, user.Username, user.Email); mailErr != nil {
		// [COMMENT]: Ghi nhận lỗi nhưng không trả về lỗi để tránh ảnh hưởng đến giao diện người dùng
	}

	// [COMMENT]: Khởi chạy background goroutine bất đồng bộ để ghi nhận presence của user mới đăng ký thành công vào Redis
	s.wg.Add(1)
	go func(bgCtx context.Context, username, email string) {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				// [COMMENT]: Bảo vệ goroutine nền không làm sập process khi xảy ra panic ngoài ý muốn
			}
		}()

		rdb := s.registry.L2.Client()
		markStart := time.Now()
		usernameDigest, markErr := security.PresenceHMACSHA256Hex("iam.register.username", username)
		var emailDigest string
		if markErr == nil {
			emailDigest, markErr = security.PresenceHMACSHA256Hex("iam.register.email", email)
		}
		if markErr == nil {
			pipe := rdb.Pipeline()
			pipe.SetBit(bgCtx, "iam:register:bitmap:username", computeBitmapIndex(usernameDigest), 1)
			pipe.SetBit(bgCtx, "iam:register:bitmap:email", computeBitmapIndex(emailDigest), 1)
			_, markErr = pipe.Exec(bgCtx)
		}
		if markErr != nil {
			// [COMMENT]: Ghi nhận metrics đo lường latency của việc lưu cache nền
			iamMetrics.Downstream(bgCtx, iamMetrics.KindCacheEngineL2, "markPresenceExists", iamMetrics.OutcomeFailureUnknown, time.Since(markStart), markErr)
		}
	}(context.WithoutCancel(ctx), user.Username, user.Email)

	return nil
}

// [COMMENT]: VerifyAccount kiểm tra tính hợp lệ của mã kích hoạt (OTT) thông qua OneTimeTokenService,
// sau đó tiến hành kích hoạt tài khoản và gán role mặc định 'platform_user' cho người dùng.
func (s *AuthService) VerifyAccount(ctx context.Context, userID uuid.UUID, token string) error {
	// [COMMENT]: Validate trước nhưng chỉ consume sau DB commit; DB lỗi không được làm mất token retry.
	valid, err := s.ott.Validate(ctx, "account_verify", userID, token)
	if err != nil {
		return err
	}
	if !valid {
		return iamTaxonomy.ErrTokenExpired
	}

	// [COMMENT]: Event ID deterministic theo user giúp HTTP retry không sinh logical event thứ hai.
	eventID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("billing.wallet.personal.provision:"+userID.String()))
	event := &iamproto.PersonalWalletProvisionRequestedV1{
		EventId:       eventID[:],
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
	if err := s.repo.ActivateUser(ctx, userID, "platform_user", eventID, payload); err != nil {
		return err
	}
	// [COMMENT]: Concurrent verify có cùng deterministic event; chỉ một request xóa token, cả hai đều idempotent.
	_, err = s.ott.Consume(ctx, "account_verify", userID, token)
	if err != nil && !errors.Is(err, iamTaxonomy.ErrTokenExpired) {
		return err
	}

	return nil
}

// [COMMENT]: Stop chờ toàn bộ các background worker/task chạy nền (như ghi presence bitmap)
// hoàn thành nhiệm vụ trước khi dừng tiến trình phục vụ Graceful Shutdown.
func (s *AuthService) Stop() {
	s.wg.Wait()
}

// [COMMENT]: pushVerifyAccountOutboxJob sinh mã One-Time Token (OTT) và chèn một bản ghi IamOutboxRecord
// để CDC worker quét và gửi mail kích hoạt tài khoản bất đồng bộ một cách đáng tin cậy.
func (s *AuthService) pushVerifyAccountOutboxJob(ctx context.Context, userID uuid.UUID, username, email string) error {
	if s.ott == nil || s.outboxRepo == nil {
		return apperr.Wrap(iamTaxonomy.ErrVerificationRequired, nil, iamMetrics.OutcomePreConditionFailed)
	}

	// [COMMENT]: Phát hành mã OTT kích hoạt tài khoản
	verificationToken, _, issueErr := s.ott.Issue(ctx, "account_verify", userID)
	if issueErr != nil {
		return fmt.Errorf("failed to issue verification token: %w", issueErr)
	}

	idempotencyKey := uuid.Must(uuid.NewV7())

	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// [COMMENT]: Đóng gói thông tin email thành Protobuf cấu hình gửi mail với đầy đủ tham số định danh, người nhận và template_id
	mailConfig := &mailproto.SendMailConfig{
		TemplateVariables: map[string]string{
			"template_id":  "platform/verify_account",
			"to":           email,
			"fullname":     username,
			"user_id":      userID.String(),
			"verify_token": verificationToken,
			"from":         "noreply@aurora.system",
		},
	}

	payloadBytes, marshalErr := proto.Marshal(mailConfig)
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal verification mail payload: %w", marshalErr)
	}

	record := &iamEntity.IamOutboxRecord{
		EventID:              idempotencyKey,
		RoutingScope:         "platform",
		JobTopic:             "mail.verify_account",
		Payload:              payloadBytes,
		UserID:               userID.String(),
		Status:               iamEntity.IamOutboxStatusPending,
		JobVersion:           1,
		ResourceID:           "verify_account",
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Ghi nhận job vào Outbox Database Table
	if insertErr := s.outboxRepo.Create(ctx, record); insertErr != nil {
		return fmt.Errorf("failed to insert verification mail record into outbox: %w", insertErr)
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
	verified, verifyErr := security.VerifyPassword(user.PasswordHash, req.Password)
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
		// [COMMENT]: Nếu tài khoản chưa kích hoạt, tự động đẩy outbox job gửi mail kích hoạt tài khoản dùng chung helper
		if err := s.pushVerifyAccountOutboxJob(ctx, user.ID, user.Username, user.Email); err != nil {
			if errors.Is(err, iamTaxonomy.ErrVerificationRequired) {
				loginOutcome = iamMetrics.OutcomePreConditionFailed
				return nil, err
			}
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
		}

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
		rawRefresh, _, refreshErr = s.refreshSvc.CreateRefreshToken(ctx, user.ID, trackedDeviceID, tenantUUIDPtr)
		if refreshErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return nil, refreshErr
		}
	}

	return &iamEntity.VerifyUserCredentialsResult{
		Valid:  true,
		UserID: user.ID.String(),
		// [COMMENT]: RoleID là UUID của role đang hoạt động, ACR sẽ inject vào JWT claims và forward qua header X-User-Role-ID
		RoleID:         user.RoleID,
		Level:          user.Level,
		TenantID:       tenantID,
		ClientDeviceID: clientDeviceID,
		RefreshToken:   rawRefresh,
		Username:       user.Username,
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

const registerBitmapSize = 1 << 20

func computeBitmapIndex(value string) int64 {
	val := strings.ToLower(strings.TrimSpace(value))
	return int64(crc32.ChecksumIEEE([]byte(val)) % registerBitmapSize)
}
