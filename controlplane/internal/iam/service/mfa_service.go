package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/infra/vault"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/proto"
	"controlplane/internal/observability"
	"controlplane/internal/security"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// MfaService quản lý toàn bộ vòng đời xác thực đa yếu tố (Multi-Factor Authentication - MFA/2FA):
// - Thiết lập ban đầu (Setup & Confirmation) với mã hóa Vault Transit.
// - Xác thực đăng nhập 2FA (Verify Login qua TOTP hoặc Recovery Code).
// - Hàng rào bảo mật chống tấn công phát lại mã TOTP (Anti-Replay Time-Step Fence qua Redis Lua Script).
// - Quản lý và tái tạo mã khôi phục (Recovery Codes).
// - Hủy kích hoạt MFA (Remove).
type MfaService struct {
	vault     *vault.Client
	repo      iamRepoInterface.MfaRepository
	authRedis *goredis.Client
	metrics   observability.WorkflowRecorder
}

// NewMfaService khởi tạo một instance mới của MfaService.
func NewMfaService(
	vaultClient *vault.Client,
	repo iamRepoInterface.MfaRepository,
	authRedis *goredis.Client,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.MfaService {
	return &MfaService{
		vault:     vaultClient,
		repo:      repo,
		authRedis: authRedis,
		metrics:   metrics,
	}
}

// ============================================================================
// 1. WORKFLOW: TRA CỨU TRẠNG THÁI MFA (STATUS LOOKUP)
// ============================================================================

// GetUserMfaStatus cho phép Platform Admin tra cứu trạng thái MFA của một người dùng bất kỳ
// dựa trên cấp bậc quyền hạn (callerLevel).
func (s *MfaService) GetUserMfaStatus(
	ctx context.Context,
	userID uuid.UUID,
	callerLevel uint8,
) (enabled bool, method string, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrNotFound) || errors.Is(err, iamTaxonomy.ErrUserNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	return s.repo.GetPlatformStatus(ctx, userID, callerLevel)
}

// GetSelfMfaStatus cho phép người dùng tự tra cứu trạng thái MFA của chính mình
// (Đang bật hay tắt, thời điểm bật, và số lượng mã khôi phục còn lại chưa sử dụng).
func (s *MfaService) GetSelfMfaStatus(
	ctx context.Context,
	userID uuid.UUID,
) (out *iamEntity.MFAUserStatus, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	setting, recoveryCount, err := s.repo.GetSelfStatus(ctx, userID)
	if errors.Is(err, iamTaxonomy.ErrPreconditionFailed) {
		// Chưa thiết lập MFA -> Trả về trạng thái DISABLED
		return &iamEntity.MFAUserStatus{Status: iamEntity.MFAStatusDisabled}, nil
	}
	if err != nil {
		return nil, err
	}

	enabledAt := setting.CreatedAt
	return &iamEntity.MFAUserStatus{
		Status:                 iamEntity.MFAStatusEnabled,
		EnabledAt:              &enabledAt,
		RecoveryCodesRemaining: recoveryCount,
	}, nil
}

// GetLoginSetting truy vấn cấu hình MFA phục vụ bước kiểm tra đăng nhập (Login Gate).
func (s *MfaService) GetLoginSetting(
	ctx context.Context,
	userID uuid.UUID,
) (*iamEntity.MFASetting, error) {
	setting, err := s.repo.LoginGateGetSetting(ctx, userID)
	if errors.Is(err, iamTaxonomy.ErrPreconditionFailed) {
		// Người dùng không bật MFA -> Không yêu cầu bước 2FA
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return setting, nil
}

// ============================================================================
// 2. WORKFLOW: THIẾT LẬP MFA BAN ĐẦU (SETUP & CONFIRMATION)
// ============================================================================

// StartSetup khởi tạo tiến trình đăng ký MFA mới cho người dùng:
// 1. Kiểm tra điều kiện tiên quyết trong DB (chưa có MFA đang hoạt động).
// 2. Sinh khóa bí mật TOTP (Base32 secret) và đường dẫn Provisioning URI cho ứng dụng Authenticator.
// 3. Mã hóa Secret TOTP bằng HashiCorp Vault Transit Engine (Envelope Encryption).
// 4. Lưu trạng thái khởi tạo tạm thời vào Redis với SetNX (TTL 10 phút) để chống Race Condition khi gọi Start đồng thời.
func (s *MfaService) StartSetup(
	ctx context.Context,
	userID uuid.UUID,
) (out *iamEntity.MFASetupResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrMFAAlreadyEnabled) {
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		} else if errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable) {
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// 1. Kiểm tra DB xem người dùng đã bật MFA chưa
	if err := s.repo.SetupStart(ctx, userID); err != nil {
		return nil, err
	}

	// 2. Sinh khóa bí mật TOTP mới
	totpResult, err := security.GenerateTOTP("Aurora", userID.String())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", iamTaxonomy.ErrGenTOTPFailed, err)
	}

	// 3. Mã hóa khóa bí mật bằng HashiCorp Vault Transit
	secretCiphertext, err := s.vault.TransitEncrypt(ctx, "iam-mfa-secret", totpResult.Secret)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", iamTaxonomy.ErrEncryptSecretFailed, err)
	}

	setupID := uuid.New()
	settingID := uuid.New()
	expiresAt := time.Now().UTC().Add(10 * time.Minute)

	// 4. Đóng gói Protobuf Envelope lưu trạng thái thiết lập tạm thời
	payload, err := proto.Marshal(&iamproto.MfaSetupPending{
		SetupId:          setupID.String(),
		UserId:           userID.String(),
		SettingId:        settingID.String(),
		SecretCiphertext: secretCiphertext,
		SecretKeyId:      "transit/iam-mfa-secret",
		SchemaVersion:    1,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode setup state: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}

	// 5. Lưu vào Redis bằng SetNX (TTL 10 phút) để đảm bảo tính nguyên tử
	stored, err := s.authRedis.SetNX(
		ctx,
		fmt.Sprintf("iam:mfa:setup:%s", userID),
		payload,
		10*time.Minute,
	).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: store setup state: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	if !stored {
		return nil, iamTaxonomy.ErrMFAAlreadyEnabled
	}

	return &iamEntity.MFASetupResult{
		SetupID:         setupID,
		ProvisioningURI: totpResult.ProvisioningURI,
		ManualSecret:    totpResult.Secret,
		ExpiresAt:       expiresAt,
	}, nil
}

// ConfirmSetup xác nhận mã OTP đầu tiên từ ứng dụng Authenticator để chính thức kích hoạt MFA:
// 1. Đọc và xác thực trạng thái Setup Pending từ Redis.
// 2. Giải mã Secret TOTP qua Vault Transit.
// 3. Xác thực mã 6 chữ số TOTP (cho phép độ trễ mạng ±1 time step = 30s).
// 4. Chạy Lua script trên Redis làm hàng rào chống phát lại mã (Anti-Replay Time-Step Fence).
// 5. Sinh 10 mã khôi phục (Recovery Codes), băm SHA-256 và lưu vĩnh viễn cấu hình MFA vào PostgreSQL.
// 6. Dọn dẹp trạng thái tạm trong Redis.
func (s *MfaService) ConfirmSetup(
	ctx context.Context,
	userID, setupID uuid.UUID,
	code string,
) (*iamEntity.MFAConfirmationResult, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// 1. Đọc bản ghi Setup Pending từ Redis
	pendingKey := fmt.Sprintf("iam:mfa:setup:%s", userID)
	pendingBytes, err := s.authRedis.Get(ctx, pendingKey).Bytes()
	if errors.Is(err, goredis.Nil) {
		result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		return nil, iamTaxonomy.ErrMFASetupExpired
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read setup state: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}

	var pending iamproto.MfaSetupPending
	if err := proto.Unmarshal(pendingBytes, &pending); err != nil {
		result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		return nil, iamTaxonomy.ErrMFASetupExpired
	}

	// 2. Xác thực tính hợp lệ của Envelope định danh
	pendingUserID, userIDErr := uuid.Parse(strings.TrimSpace(pending.GetUserId()))
	pendingSetupID, setupIDErr := uuid.Parse(strings.TrimSpace(pending.GetSetupId()))
	pendingSettingID, settingIDErr := uuid.Parse(strings.TrimSpace(pending.GetSettingId()))

	if userIDErr != nil || setupIDErr != nil || settingIDErr != nil ||
		pendingUserID != userID || pendingSetupID != setupID ||
		pendingSettingID == uuid.Nil ||
		pending.GetSchemaVersion() != 1 ||
		strings.TrimSpace(pending.GetSecretCiphertext()) == "" ||
		strings.TrimSpace(pending.GetSecretKeyId()) == "" {
		result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		return nil, iamTaxonomy.ErrMFASetupExpired
	}

	// 3. Giải mã khóa bí mật TOTP qua Vault Transit
	secret, err := s.vault.TransitDecrypt(ctx, "iam-mfa-secret", pending.GetSecretCiphertext())
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt setup state: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}

	// 4. Kiểm tra mã TOTP trong khung thời gian [t-30s, t, t+30s]
	now := time.Now().UTC()
	currentStep := now.Unix() / 30
	acceptedStep := int64(-1)

	for offset := int64(-1); offset <= 1; offset++ {
		candidateStep := currentStep + offset
		valid, validateErr := totp.ValidateCustom(code, secret, time.Unix(candidateStep*30, 0).UTC(), totp.ValidateOpts{
			Period: 30,
			Skew:   0,
			Digits: otp.DigitsSix,
		})
		if validateErr == nil && valid {
			acceptedStep = candidateStep
			break
		}
	}
	if acceptedStep < 0 {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, iamTaxonomy.ErrMFAInvalidCode
	}

	// 5. Khóa Anti-Replay Time-Step trên Redis (Ngăn chặn dùng lại cùng 1 mã OTP trong cùng khung 30s)
	totpFenceKey := fmt.Sprintf("iam:mfa:totp:%s:%s", userID, pendingSettingID)
	reserved, err := s.authRedis.Eval(ctx, `
		local current = redis.call("GET", KEYS[1])
		if current and tonumber(current) >= tonumber(ARGV[1]) then return 0 end
		redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
		return 1
	`, []string{totpFenceKey}, acceptedStep, int64(120)).Int64()
	if err != nil {
		return nil, fmt.Errorf("%w: reserve setup totp step: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	if reserved != 1 {
		result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		return nil, iamTaxonomy.ErrMFAInvalidCode
	}

	// 6. Tạo 10 mã khôi phục (Recovery Codes: 16 ký tự an toàn) và băm SHA-256
	recoveryCodes := make([]string, 10)
	recoveryHashes := make([]string, 10)
	for i := range recoveryCodes {
		recoveryCodes[i], err = security.GenerateRecoveryCode(16)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", iamTaxonomy.ErrGenRecoveryCodeFailed, err)
		}
		recoveryHashes[i] = security.HashRecoveryCode(recoveryCodes[i])
	}

	// 7. Ghi nhận kích hoạt MFA thành công vào PostgreSQL
	enabledAt, err := s.repo.SetupConfirmEnable(
		ctx,
		userID,
		pendingSettingID,
		pending.GetSecretCiphertext(),
		pending.GetSecretKeyId(),
		recoveryHashes,
	)
	if err != nil {
		return nil, err
	}

	// 8. Dọn dẹp bản ghi Setup Pending trong Redis (Compare-and-Delete)
	_, _ = s.authRedis.Eval(ctx, `
		if redis.call("GET", KEYS[1]) ~= ARGV[1] then return 0 end
		return redis.call("DEL", KEYS[1])
	`, []string{pendingKey}, string(pendingBytes)).Int64()

	result, reason = observability.ResultSuccess, observability.ReasonNone
	return &iamEntity.MFAConfirmationResult{
		EnabledAt:     enabledAt,
		RecoveryCodes: recoveryCodes,
	}, nil
}

// ============================================================================
// 3. WORKFLOW: TÁI TẠO MÃ KHÔI PHỤC (REGENERATE RECOVERY CODES)
// ============================================================================

// RegenerateRecoveryCodes cho phép người dùng sinh lại bộ 10 mã khôi phục mới:
// 1. Xác thực bắt buộc bằng mã TOTP hiện tại kèm hàng rào chống phát lại Anti-Replay.
// 2. Sinh 10 mã khôi phục mới, băm SHA-256 và thay thế toàn bộ mã cũ trong DB.
func (s *MfaService) RegenerateRecoveryCodes(
	ctx context.Context,
	userID uuid.UUID,
	code string,
) (out []string, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrMFAInvalidCode), errors.Is(err, iamTaxonomy.ErrRecoveryCodeInvalid):
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		case errors.Is(err, iamTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		case errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// 1. Lấy cấu hình MFA hiện tại từ PostgreSQL
	setting, err := s.repo.RecoveryRegenerateGetSetting(ctx, userID)
	if err != nil {
		return nil, err
	}

	// 2. Giải mã Secret TOTP qua Vault Transit
	secret, err := s.vault.TransitDecrypt(ctx, "iam-mfa-secret", setting.SecretCiphertext)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt mfa secret: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}

	// 3. Xác thực mã TOTP
	now := time.Now().UTC()
	currentStep := now.Unix() / 30
	acceptedStep := int64(-1)

	for offset := int64(-1); offset <= 1; offset++ {
		candidateStep := currentStep + offset
		valid, validateErr := totp.ValidateCustom(code, secret, time.Unix(candidateStep*30, 0).UTC(), totp.ValidateOpts{
			Period: 30,
			Skew:   0,
			Digits: otp.DigitsSix,
		})
		if validateErr == nil && valid {
			acceptedStep = candidateStep
			break
		}
	}
	if acceptedStep < 0 {
		return nil, iamTaxonomy.ErrMFAInvalidCode
	}

	// 4. Khóa Anti-Replay Time-Step trên Redis
	totpFenceKey := fmt.Sprintf("iam:mfa:totp:%s:%s", userID, setting.ID)
	reserved, err := s.authRedis.Eval(ctx, `
		local current = redis.call("GET", KEYS[1])
		if current and tonumber(current) >= tonumber(ARGV[1]) then return 0 end
		redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
		return 1
	`, []string{totpFenceKey}, acceptedStep, int64(120)).Int64()
	if err != nil {
		return nil, fmt.Errorf("%w: reserve regenerate totp step: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	if reserved != 1 {
		return nil, iamTaxonomy.ErrMFAInvalidCode
	}

	// 5. Sinh 10 mã khôi phục mới và băm SHA-256
	recoveryCodes := make([]string, 10)
	recoveryHashes := make([]string, 10)
	for i := range recoveryCodes {
		recoveryCodes[i], err = security.GenerateRecoveryCode(16)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", iamTaxonomy.ErrGenRecoveryCodeFailed, err)
		}
		recoveryHashes[i] = security.HashRecoveryCode(recoveryCodes[i])
	}

	// 6. Ghi đè bộ mã khôi phục mới vào DB
	if err := s.repo.RecoveryRegenerateReplace(ctx, userID, recoveryHashes); err != nil {
		return nil, err
	}

	return recoveryCodes, nil
}

// ============================================================================
// 4. WORKFLOW: HỦY KÍCH HOẠT MFA (REMOVE MFA)
// ============================================================================

// Remove thực hiện vô hiệu hóa và xóa bỏ cấu hình MFA của người dùng:
// 1. Xác thực bằng mã TOTP hiện tại kèm hàng rào chống phát lại.
// 2. Xóa sạch cài đặt MFA và mã khôi phục tương ứng trong DB.
func (s *MfaService) Remove(
	ctx context.Context,
	userID uuid.UUID,
	code string,
) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrMFAInvalidCode):
			result, reason = observability.ResultRejected, observability.ReasonUnauthenticated
		case errors.Is(err, iamTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		case errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable):
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// 1. Lấy cấu hình MFA hiện tại
	setting, err := s.repo.RemoveGetSetting(ctx, userID)
	if err != nil {
		return err
	}

	// 2. Giải mã Secret TOTP qua Vault Transit
	secret, err := s.vault.TransitDecrypt(ctx, "iam-mfa-secret", setting.SecretCiphertext)
	if err != nil {
		return fmt.Errorf("%w: decrypt mfa secret: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}

	// 3. Xác thực mã TOTP
	now := time.Now().UTC()
	currentStep := now.Unix() / 30
	acceptedStep := int64(-1)

	for offset := int64(-1); offset <= 1; offset++ {
		candidateStep := currentStep + offset
		valid, validateErr := totp.ValidateCustom(code, secret, time.Unix(candidateStep*30, 0).UTC(), totp.ValidateOpts{
			Period: 30,
			Skew:   0,
			Digits: otp.DigitsSix,
		})
		if validateErr == nil && valid {
			acceptedStep = candidateStep
			break
		}
	}
	if acceptedStep < 0 {
		return iamTaxonomy.ErrMFAInvalidCode
	}

	// 4. Khóa Anti-Replay Time-Step trên Redis
	totpFenceKey := fmt.Sprintf("iam:mfa:totp:%s:%s", userID, setting.ID)
	reserved, err := s.authRedis.Eval(ctx, `
		local current = redis.call("GET", KEYS[1])
		if current and tonumber(current) >= tonumber(ARGV[1]) then return 0 end
		redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
		return 1
	`, []string{totpFenceKey}, acceptedStep, int64(120)).Int64()
	if err != nil {
		return fmt.Errorf("%w: reserve remove totp step: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}
	if reserved != 1 {
		return iamTaxonomy.ErrMFAInvalidCode
	}

	// 5. Xóa cấu hình MFA trong PostgreSQL
	return s.repo.RemoveDelete(ctx, userID)
}

// ============================================================================
// 5. WORKFLOW: XÁC THỰC ĐĂNG NHẬP 2FA (VERIFY LOGIN CHALLENGE)
// ============================================================================

// VerifyLogin xác thực thử thách 2FA trong quá trình đăng nhập của người dùng:
// - Phương thức "recovery_code": Tiêu thụ 1 mã khôi phục (đánh dấu đã sử dụng trong DB).
// - Phương thức "totp": Giải mã secret qua Vault Transit, xác thực mã 6 số và khóa Time-Step chống Replay trên Redis.
func (s *MfaService) VerifyLogin(
	ctx context.Context,
	userID, settingID uuid.UUID,
	method, code string,
) error {
	// 1. Kiểm tra cấu hình MFA đang kích hoạt
	setting, err := s.repo.LoginVerifyGetSetting(ctx, userID)
	if err != nil {
		return iamTaxonomy.ErrMFAChallengeInvalid
	}
	if setting.ID != settingID {
		return iamTaxonomy.ErrMFAChallengeInvalid
	}

	switch method {
	case "recovery_code":
		// Xác thực và tiêu thụ mã khôi phục (Mỗi mã chỉ được dùng 1 lần)
		if consumeErr := s.repo.LoginConsumeRecoveryCode(
			ctx,
			userID,
			settingID,
			security.HashRecoveryCode(code),
		); consumeErr != nil {
			return consumeErr
		}
		return nil

	case "totp":
		// 1. Giải mã Secret TOTP qua Vault Transit
		secret, decryptErr := s.vault.TransitDecrypt(ctx, "iam-mfa-secret", setting.SecretCiphertext)
		if decryptErr != nil {
			return fmt.Errorf("%w: decrypt mfa secret: %v", iamTaxonomy.ErrAuthenticationUnavailable, decryptErr)
		}

		// 2. Xác thực mã TOTP (Khung thời gian ±30s)
		now := time.Now().UTC()
		currentStep := now.Unix() / 30
		acceptedStep := int64(-1)

		for offset := int64(-1); offset <= 1; offset++ {
			candidateStep := currentStep + offset
			valid, validateErr := totp.ValidateCustom(code, secret, time.Unix(candidateStep*30, 0).UTC(), totp.ValidateOpts{
				Period: 30,
				Skew:   0,
				Digits: otp.DigitsSix,
			})
			if validateErr == nil && valid {
				acceptedStep = candidateStep
				break
			}
		}
		if acceptedStep < 0 {
			return iamTaxonomy.ErrMFAInvalidCode
		}

		// 3. Khóa Anti-Replay Time-Step trên Redis
		totpFenceKey := fmt.Sprintf("iam:mfa:totp:%s:%s", userID, settingID)
		reserved, reserveErr := s.authRedis.Eval(ctx, `
			local current = redis.call("GET", KEYS[1])
			if current and tonumber(current) >= tonumber(ARGV[1]) then return 0 end
			redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
			return 1
		`, []string{totpFenceKey}, acceptedStep, int64(120)).Int64()
		if reserveErr != nil {
			return fmt.Errorf("%w: reserve login totp step: %v", iamTaxonomy.ErrAuthenticationUnavailable, reserveErr)
		}
		if reserved != 1 {
			return iamTaxonomy.ErrMFAInvalidCode
		}

		return nil

	default:
		return iamTaxonomy.ErrMFAChallengeInvalid
	}
}
