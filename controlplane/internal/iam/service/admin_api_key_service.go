// ======================================================================================================
// 📂 MODULE: controlplane/internal/iam/service/admin_api_key_service.go
//            Đặc Tả Nghiệp Vụ Quản Trị Hệ Thống & Xác Thực Admin
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & PHÂN CHIA QUYỀN LỰC (CONTRACT & ARCHITECTURAL PLANES):
//   - Hệ thống Controlplane được phân chia rõ rệt thành 2 mặt phẳng quyền lực (2 Planes of Power)
//     hoàn toàn độc lập tuyệt đối:
//
//     1) MẶT PHẲNG QUẢN TRỊ HẠ TẦNG (INFRASTRUCTURE MANAGEMENT PLANE - Phân hệ hiện tại):
//        * Do `AdminAPIKeyService` chịu trách nhiệm điều phối độc quyền toàn bộ vòng đời quyền truy
//          cập quản trị: Bootstrap → Login → Session Refresh → Logout → Emergency Key Rotation.
//        * Lối đi bảo mật tối cao, tách biệt hoàn toàn, dành riêng cho Operator/SRE và các hệ thống
//          tự động điều hành hạ tầng cốt lõi (cấu hình dataplane, telemetry pipeline, key rotation).
//        * Xác thực 2 lớp cứng (Hard 2FA): API Key thô được so sánh bằng hash SHA256 an toàn + đa
//          yếu tố MFA bắt buộc (TOTP 6 chữ số hoặc Recovery Code sử dụng một lần).
//        * Liên kết vật lý thiết bị bất biến (Device Binding): Mỗi phiên Admin bị gắn chặt vào một
//          khóa Ed25519 duy nhất đã đăng ký (`AdminDeviceBinding`) để chống token hijacking.
//        * Không đi qua bất kỳ tầng RBAC nào. Quyền truy cập là nhị phân: có Admin key hợp lệ thì
//          toàn quyền điều hành hạ tầng, không có thì từ chối tuyệt đối.
//        * Phiên Admin (Admin Session) được quản lý qua cặp `access_key`/`access_secret` lưu trong
//          Redis, được ký thành JWT ngắn hạn với family `SecretFamilyAdminAPIKey`.
//
//     2) MẶT PHẲNG NGHIỆP VỤ NGƯỜI DÙNG VÀ NỀN TẢNG (USER & PLATFORM PLANE):
//        * Do `AuthService` (xem auth_service.go) quản lý. Dành cho Customer và các tài khoản nền
//          tảng hệ thống (root level 0, sys_admin level 1, support level 2 — xem auth_service.go).
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Khóa API Key và thiết lập TOTP Secret được lưu trữ mã hóa và xác thực duy nhất trong
//     Postgres Database.
//   - Phiên Admin dùng mô hình **Fragment Token** (3 mảnh) thay vì 1 access token đơn thông thường:
//     Mảnh 1 — JWT access token ngắn hạn (stateless, ký bằng `SecretFamilyAdminAPIKey`).
//     Mảnh 2 — `access_key`: định danh phiên, lưu trong Redis (`AdminDeviceRuntimeCache`).
//     Mảnh 3 — `access_secret`: bí mật phiên, chỉ lưu dạng hash trong Redis.
//     Middleware phải xác thực cả 3 mảnh khớp nhau thì phiên mới được coi là hợp lệ.
//
// 🔒 RANH GIỚI BẢO MẬT NGHIÊM NGẶT (CRITICAL SECURITY BOUNDARY):
//   - **Mật mã & Plaintext**: Khóa API Key thô (Plaintext API Key) và TOTP Secret thô của Admin
//     **TUYỆT ĐỐI KHÔNG** được phép lưu trữ dưới dạng văn bản thuần túy trong database, không được ghi
//     nhận trong bất kỳ log hệ thống nào. Chúng chỉ tồn tại tạm thời trong RAM của luồng xác thực
//     hoặc được gửi trực tiếp qua kênh thông báo khẩn cấp an toàn (Telegram Client).
//   - **Tấn công Race Condition**: Ngăn chặn tuyệt đối việc sử dụng đồng thời một Mã khôi phục
//     (Recovery Code) qua Distributed Lock của Redis (`AcquireRecoveryConsumeLock`).
//   - **Phân tách quyền**: Service này chỉ thực hiện xử lý logic nghiệp vụ và telemetry, không tự động
//     bắt lỗi hoặc ghi đè phản hồi HTTP. Toàn bộ lỗi được đóng gói dạng `apperr.Wrap` và trả về tầng
//     Handler để định cấu trúc mã lỗi HTTP.
//
// 🔄 CALLSITE FLOW:
//   - Được gọi trực tiếp bởi `AdminAuthHandler` tại `controlplane/internal/iam/transport/http/handler/
//     admin_auth_handler.go` để xử lý các yêu cầu HTTP Login, Session, Refresh, Logout, Rotate.
//   - Được gọi bởi scheduler/operator định kỳ (`TryProcessAdminKeyRotationTrigger`) để kiểm tra cờ
//     rotation.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Master Secrets (`security.SecretProvider`) phải sẵn sàng và được phân giải chính xác để ký mã hóa
//     token với family `SecretFamilyAdminAPIKey`.
//   - Mọi hoạt động cập nhật Last Seen của thiết bị (`TouchAdminDeviceLastSeen`) chỉ được thực hiện
//     khi có cờ `LastSeenDirty = true` để tối ưu hóa hiệu năng và tránh ghi đè Database liên tục trên
//     mỗi Request (Lazy Flush).
//
// ======================================================================================================

package iamSvcImpl

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"controlplane/infra/telegram"
	"controlplane/internal/config"
	iamCache "controlplane/internal/iam/cache"
	deviceHint "controlplane/internal/iam/devicehint"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	"errors"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// AdminAPIKeyService implements business flow cho:
// - bootstrap admin key,
// - login admin qua API key + MFA,
// - logout admin runtime.
//
// Boundary:
// - Service không log nghiệp vụ.
// - Error được trả lên handler để mapping HTTP.
type AdminAPIKeyService struct {
	cfg             *config.Config
	repo            iamRepoInterface.AdminAPIKeyRepository
	telegram        *telegram.TelegramClient
	secrets         security.SecretProvider
	deviceRT        iamCache.AdminDeviceRuntimeCache
	apiKeyCache     iamCache.AdminAPIKeyCache
	rotationTrigger iamCache.AdminKeyRotationTriggerCache
}

// NewAdminAPIKeyService tạo service instance với các dependency cần thiết cho
// bootstrap/login/logout flow.
func NewAdminAPIKeyService(
	cfg *config.Config,
	repo iamRepoInterface.AdminAPIKeyRepository,
	telegram *telegram.TelegramClient,
	secrets security.SecretProvider,
	deviceRT iamCache.AdminDeviceRuntimeCache,
	apiKeyCache iamCache.AdminAPIKeyCache,
	rotationTrigger ...iamCache.AdminKeyRotationTriggerCache,
) iamSvcInterface.AdminAPIKeyService {
	var trigger iamCache.AdminKeyRotationTriggerCache
	if len(rotationTrigger) > 0 {
		trigger = rotationTrigger[0]
	}
	return &AdminAPIKeyService{
		cfg:             cfg,
		repo:            repo,
		telegram:        telegram,
		secrets:         secrets,
		deviceRT:        deviceRT,
		apiKeyCache:     apiKeyCache,
		rotationTrigger: trigger,
	}
}

// RotateAdminAPIKeyEmergency thực thi rotation admin API key khẩn cấp
// (compromise hoặc theo trigger từ scheduler/operator).
//
// Flow:
//  1. acquire rotation lock toàn cục để chống đồng thời,
//  2. sinh API key mới + tính hash + expiry,
//  3. notify plaintext key qua Telegram trước khi persist,
//  4. persist key mới vào DB ở trạng thái next-active,
//  5. invalidate RAM cache active key + clear rotation trigger.
//
// Boundary:
// - Plaintext key chỉ tồn tại trong RAM + Telegram message, không log.
// - Nếu Telegram fail thì abort để tránh ghi DB mà operator không nhận được key.
func (s *AdminAPIKeyService) RotateAdminAPIKeyEmergency(ctx context.Context, actor string) error {
	lock, err := s.repo.AcquireRotationLock(ctx)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already held") {
			return apperr.Wrap(iamTaxonomy.ErrAdminRotationLockBusy, err, "lock_busy")
		}
		return apperr.Wrap(iamTaxonomy.ErrAdminRotationFailed, err, "dependency_error")
	}
	defer lock.Release(ctx)

	plainKey, err := security.GenerateToken(48)
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminRotationFailed, err, "dependency_error")
	}
	keyHash := security.HashTokenSHA256(plainKey)
	expiresAt := time.Now().UTC().Add(s.cfg.Security.AdminAPITokenTTL)

	msg := fmt.Sprintf("<b>ADMIN ROTATION SUCCESS</b>\nAPI Key: <code>%s</code>\nExpires: <code>%s</code>", plainKey, expiresAt.Format(time.RFC3339))
	if sendErr := s.telegram.SendMessage(msg); sendErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminRotationDelivery, sendErr, "delivery_error")
	}

	if err := s.repo.PrepareNextAdminAPIKey(ctx, actor, keyHash, expiresAt); err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminRotationFailed, err, "dependency_error")
	}
	if s.apiKeyCache != nil {
		s.apiKeyCache.InvalidateActiveAPIKey()
	}
	if s.rotationTrigger != nil {
		_ = s.rotationTrigger.ClearRotationRequired(ctx)
	}
	return nil
}

// TryProcessAdminKeyRotationTrigger kiểm tra cờ rotation trong cache và chạy
// rotation nếu cờ được bật.
//
// Mục tiêu:
// - được scheduler gọi định kỳ (poll trigger),
// - no-op khi rotationTrigger không được wire hoặc trigger chưa bật.
func (s *AdminAPIKeyService) TryProcessAdminKeyRotationTrigger(ctx context.Context) error {
	if s.rotationTrigger == nil {
		return nil
	}
	required, err := s.rotationTrigger.HasRotationRequired(ctx)
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminRotationFailed, err, "trigger_error")
	}
	if !required {
		return nil
	}
	if err := s.RotateAdminAPIKeyEmergency(ctx, "system-scheduler"); err != nil {
		return err
	}
	return nil
}

// Bootstrap khởi tạo admin API key đầu tiên cho hệ thống.
//
// Flow:
// 1) lock bootstrap toàn cục,
// 2) kiểm tra chưa có key active,
// 3) tạo API key + TOTP secret + recovery codes,
// 4) persist DB,
// 5) notify Telegram,
// 6) nếu notify fail thì rollback DB.
func (s *AdminAPIKeyService) Bootstrap(ctx context.Context, actor string) error {
	lock, err := s.repo.AcquireBootstrapLock(ctx)
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminBootstrapLockFailed, err, "lock_failed")
	}
	defer lock.Release(ctx)

	now := time.Now().UTC()
	active, err := s.loadActiveAdminAPIKey(ctx)
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminBootstrapPreconditionFailed, err, "precondition_failed")
	}
	if active != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminBootstrapNotAllowed, nil, "not_allowed")
	}

	expiresAt := now.Add(s.cfg.Security.AdminAPITokenTTL)

	plainKey, err := security.GenerateToken(48)
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminBootstrapPersistFailed, err, "persist_failed")
	}
	keyHash := security.HashTokenSHA256(plainKey)

	totpResult, err := security.GenerateTOTP("controlplane-admin", "admin@system")
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminBootstrapPersistFailed, err, "persist_failed")
	}
	secretCipher, err := security.EncryptSecret(totpResult.Secret)
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminBootstrapPersistFailed, err, "persist_failed")
	}

	recoveryHashes := make([]string, 0, 8)
	recoveryPlains := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		code, genErr := security.GenerateRecoveryCode(24)
		if genErr != nil {
			return apperr.Wrap(iamTaxonomy.ErrAdminBootstrapPersistFailed, genErr, "persist_failed")
		}
		recoveryPlains = append(recoveryPlains, code)
		recoveryHashes = append(recoveryHashes, security.HashRecoveryCode(code))
	}

	payload := iamEntity.AdminBootstrapPayload{
		Actor:              actor,
		KeyHash:            keyHash,
		ExpiresAt:          expiresAt,
		RecoveryCodeHashes: recoveryHashes,
		GeneratedAt:        now,
		SecretCiphertext:   secretCipher,
	}
	_, err = s.repo.Bootstrap(ctx, payload)
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminBootstrapPersistFailed, err, "persist_failed")
	}

	msg := fmt.Sprintf(
		"<b>✅ ADMIN BOOTSTRAP READY</b>\n\n"+
			"<b>🔐 API Key</b>\n<code>%s</code>\n\n"+
			"<b>⏰ Expires At (UTC)</b>\n<code>%s</code>\n\n"+
			"<b>🛡️ TOTP Secret</b>\n<code>%s</code>\n\n"+
			"<b>🧩 Recovery Codes (one-time)</b>\n%s\n\n"+
			"<i>Store these secrets in a secure vault. Do not share.</i>",
		plainKey,
		expiresAt.Format(time.RFC3339),
		totpResult.Secret,
		formatRecoveryCodesForTelegram(recoveryPlains),
	)
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	var notifyErr error
	for i, wait := range backoff {
		if sendErr := s.telegram.SendMessage(msg); sendErr == nil {
			return nil
		} else {
			notifyErr = sendErr
		}
		if i < len(backoff)-1 {
			time.Sleep(wait)
		}
	}

	if rbErr := s.repo.RollbackBootstrap(ctx, payload); rbErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrAdminBootstrapRollbackFailed, rbErr, "rollback_failed")
	}
	return apperr.Wrap(iamTaxonomy.ErrAdminBootstrapNotifyFailed, notifyErr, "notify_failed")
}

// AdminLogin xác thực admin credentials và cấp runtime fragments cho `/admin`.
//
// Input business:
// - raw API key,
// - MFA method + MFA code,
// - device public key.
//
// Output business:
// - admin_api_token (JWT),
// - access_key,
// - access_secret,
// - expires_at.
func (s *AdminAPIKeyService) AdminLogin(ctx context.Context, req iamEntity.AdminLoginRequest) (iamEntity.AdminLoginResult, error) {
	// Khởi tạo trạng thái kết quả đăng nhập mặc định là thành công (OutcomeSuccess)
	// Trạng thái này sẽ được cập nhật tương ứng nếu phát hiện lỗi ở các bước sau.
	loginOutcome := iamTaxonomy.OutcomeSuccess
	// Sử dụng defer để tự động ghi nhận chỉ số telemetry (metrics) kết quả đăng nhập của Admin khi hàm kết thúc.
	defer func() { iamMetrics.ObserveAdminLoginOutcome(loginOutcome) }()

	// --- BƯỚC 1: XÁC THỰC THAM SỐ ĐẦU VÀO (INPUT VALIDATION) ---
	// Đảm bảo không có trường dữ liệu bắt buộc nào (API Key, MFA Method, MFA Code, Device Public Key) bị trống.
	if strings.TrimSpace(req.RawAPIKey) == "" || strings.TrimSpace(req.MFAMethod) == "" || strings.TrimSpace(req.MFACode) == "" || strings.TrimSpace(req.DevicePublicKey) == "" {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidArgument
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, iamTaxonomy.AdminLoginOutcomeInvalidArgument)
	}

	// --- BƯỚC 2: CHUẨN HÓA VÀ KIỂM TRA KHÓA CÔNG KHAI THIẾT BỊ (DEVICE PUBLIC KEY NORMALIZATION) ---
	// Chuẩn hóa chuỗi khóa công khai Ed25519 nhận được từ Client sang định dạng chuẩn thống nhất.
	canonicalPublicKey, keyErr := normalizeDevicePublicKey(strings.TrimSpace(req.DevicePublicKey))
	if keyErr != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidDevicePublicKey
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, keyErr, iamTaxonomy.AdminLoginOutcomeInvalidArgument)
	}

	// Ghi nhận mốc thời gian UTC hiện tại cho mọi giao dịch lưu trữ và xác thực
	now := time.Now().UTC()

	// --- BƯỚC 3: XÁC THỰC API KEY ADMIN (API KEY VERIFICATION) ---
	// Nạp API Key đang hoạt động của Admin từ bộ nhớ đệm 2 lớp (Cache-aside pattern: RAM/Redis hoặc DB).
	active, err := s.loadActiveAdminAPIKey(ctx)
	if err != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeLoadActiveKeyError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.AdminLoginOutcomeLoadActiveKeyError)
	}
	// Nếu không tồn tại API Key nào đang hoạt động trong hệ thống, từ chối đăng nhập.
	if active == nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidCredential
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginInvalidCredential, nil, iamTaxonomy.AdminLoginOutcomeInvalidCredential)
	}
	// Thực hiện băm SHA256 API Key của Client gửi lên và đối khớp trực tiếp với KeyHash được cấu hình bảo mật.
	if active.KeyHash != security.HashTokenSHA256(req.RawAPIKey) {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidCredential
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginInvalidCredential, nil, iamTaxonomy.AdminLoginOutcomeInvalidCredential)
	}

	// --- BƯỚC 4: XÁC THỰC ĐA YẾU TỐ (MFA VALIDATION) ---
	mfaMethod := strings.TrimSpace(strings.ToLower(req.MFAMethod))
	switch mfaMethod {
	case "totp":
		// Phân hệ TOTP: Nạp khóa bí mật 2FA của Admin từ cơ sở dữ liệu.
		secret, secErr := s.loadAdminTOTPSecret(ctx)
		if secErr != nil {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeLoadTOTPSecretError
			return iamEntity.AdminLoginResult{}, secErr
		}

		code := strings.TrimSpace(req.MFACode)
		secret = strings.TrimSpace(secret)

		// Ngăn chặn mã xác thực hoặc khóa bí mật trống.
		if code == "" {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeMFAInvalidEmptyCode
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, iamTaxonomy.AdminLoginOutcomeMFAInvalidEmptyCode)
		}
		if secret == "" {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeMFAInvalidEmptySecret
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, iamTaxonomy.AdminLoginOutcomeMFAInvalidEmptySecret)
		}

		// Thực hiện giải mã cryptographic secret và đối khớp mã TOTP với chu kỳ 30 giây và sai số lệch giờ cho phép (Skew) là 1 chu kỳ.
		okTOTP, totpErr := totp.ValidateCustom(code, secret, now, totp.ValidateOpts{
			Period:    30,
			Skew:      1,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if totpErr != nil {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeMFAValidateError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, totpErr, iamTaxonomy.AdminLoginOutcomeMFAValidateError)
		}
		if !okTOTP {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeMFAInvalidCodeOrTimeSkew
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, iamTaxonomy.AdminLoginOutcomeMFAInvalidCodeOrTimeSkew)
		}

	case "recovery_code":
		// Phân hệ Recovery Code (Mã Khôi Phục): Thực hiện băm SHA256 mã khôi phục để đối khớp.
		codeHash := security.HashRecoveryCode(req.MFACode)

		// Để triệt tiêu hoàn toàn tấn công Race Condition (Double-Consume Attack),
		// hệ thống sử dụng Distributed Lock của Redis qua phương thức AcquireRecoveryConsumeLock.
		unlock, lockErr := s.apiKeyCache.AcquireRecoveryConsumeLock(ctx, codeHash, 5*time.Second)
		if lockErr != nil {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeRecoveryLockError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, lockErr, iamTaxonomy.AdminLoginOutcomeRecoveryLockError)
		}
		defer unlock()

		// Thực hiện tiêu hủy mã khôi phục trong Database (chỉ cho phép sử dụng duy nhất một lần).
		consumed, consumeErr := s.repo.ConsumeRecoveryCode(ctx, codeHash, now)
		if consumeErr != nil {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeConsumeRecoveryError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, consumeErr, iamTaxonomy.AdminLoginOutcomeConsumeRecoveryError)
		}
		if !consumed {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeMFAInvalid
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, iamTaxonomy.AdminLoginOutcomeMFAInvalid)
		}

	default:
		// Từ chối nếu phương thức MFA không thuộc các loại được hỗ trợ.
		loginOutcome = iamTaxonomy.AdminLoginOutcomeMFAInvalid
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, iamTaxonomy.AdminLoginOutcomeMFAInvalid)
	}

	// --- BƯỚC 5: TẠO CẶP ĐỊNH DANH PHIÊN CHẠY (RUNTIME SESSION CONFIGURATION) ---
	// Sinh mã UUID ngẫu nhiên cho access_key (DeviceID) để định danh phiên làm việc.
	deviceID := uuid.NewString()
	// Sinh khóa bí mật của thiết bị (access_secret / Device Secret) ngẫu nhiên dài 48 bytes có tính mật mã bảo mật cao.
	deviceSecret, genErr := security.GenerateToken(48)
	if genErr != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeDeviceSecretIssueError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, genErr, iamTaxonomy.AdminLoginOutcomeDeviceSecretIssueError)
	}

	// Khởi tạo bản ghi trạng thái phiên tạm thời trong Redis.
	tokenJTI := ""
	if err := s.deviceRT.SetDeviceRuntime(ctx, iamCache.AdminDeviceRuntime{
		DeviceID:         deviceID,
		DeviceSecretHash: security.HashTokenSHA256(deviceSecret),
		TrackedDeviceID:  "",
		TokenJTI:         tokenJTI,
		Version:          1,
	}, s.cfg.Security.AdminSessionTTL); err != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeRuntimeCacheError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, err, iamTaxonomy.AdminLoginOutcomeRuntimeCacheError)
	}

	// --- BƯỚC 6: ĐĂNG KÝ VÀ LIÊN KẾT THIẾT BỊ VẬT LÝ VỚI DATABASE ---
	// Tạo dấu vân tay (Fingerprint) của khóa công khai bằng hàm băm SHA256.
	fp := sha256.Sum256([]byte(canonicalPublicKey))
	fph := hex.EncodeToString(fp[:])

	// Phân giải tên thiết bị gợi ý từ Client (HostnameHint & HostnameAlias).
	deviceName := deviceHint.ResolveDeviceName(req.HostnameHint, req.HostnameAlias)
	// Phân giải mã định danh thiết bị trình duyệt dài hạn (ClientDeviceID).
	clientDeviceID, clientDeviceProvenance := deviceHint.ResolveClientDeviceID(req.ClientDeviceID)
	cdidPtr := clientDeviceID

	// Ghi nhận thiết bị vật lý của Admin xuống Postgres Database.
	deviceBinding, bindErr := s.repo.UpsertAdminDeviceBinding(ctx, iamEntity.AdminDeviceBindingInput{
		DeviceName:           deviceName,
		PublicKey:            canonicalPublicKey,
		PublicKeyFingerprint: fph,
		ClientDeviceID:       &cdidPtr,
		Now:                  now,
	})
	if bindErr != nil {
		// Rollback: Xóa bản ghi runtime session đã ghi nhận trước đó ở Redis nếu việc binding DB thất bại.
		_ = s.deviceRT.DeleteDeviceSecret(ctx, deviceID)
		loginOutcome = iamTaxonomy.AdminLoginOutcomeUpsertDeviceBindingErr
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginDeviceBindingFailed, bindErr, iamTaxonomy.AdminLoginOutcomeUpsertDeviceBindingErr)
	}

	// --- BƯỚC 7: THIẾT LẬP VÀ LIÊN KẾT PHIÊN LÀM VIỆC CHÍNH THỨC ---
	// Kiểm tra sự sẵn có của Master Secrets của hệ thống để thực hiện ký số.
	if s.secrets == nil {
		_ = s.deviceRT.DeleteDeviceSecret(ctx, deviceID)
		loginOutcome = iamTaxonomy.AdminLoginOutcomeAuthUnavailable
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, nil, iamTaxonomy.AdminLoginOutcomeAuthUnavailable)
	}

	// Sinh Token ID duy nhất sử dụng UUIDv7 để phục vụ JTI Claim giúp định danh chính xác JWT.
	adminJTI, jtiErr := uuid.NewV7()
	if jtiErr != nil {
		_ = s.deviceRT.DeleteDeviceSecret(ctx, deviceID)
		loginOutcome = iamTaxonomy.AdminLoginOutcomeJTIIssueError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, jtiErr, iamTaxonomy.AdminLoginOutcomeJTIIssueError)
	}
	tokenJTI = adminJTI.String()

	// Cập nhật lại bản ghi Live Runtime Session hoàn chỉnh trong Redis Cache.
	if err := s.deviceRT.SetDeviceRuntime(ctx, iamCache.AdminDeviceRuntime{
		DeviceID:         deviceID,
		DeviceSecretHash: security.HashTokenSHA256(deviceSecret),
		TrackedDeviceID:  deviceBinding.ID.String(),
		DevicePublicKey:  canonicalPublicKey,
		TokenJTI:         tokenJTI,
		Version:          1,
		LastSeenAt:       now.UTC().Unix(),
	}, s.cfg.Security.AdminSessionTTL); err != nil {
		_ = s.deviceRT.DeleteDeviceSecret(ctx, deviceID)
		loginOutcome = iamTaxonomy.AdminLoginOutcomeRuntimeUpdateError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, err, iamTaxonomy.AdminLoginOutcomeRuntimeUpdateError)
	}

	// --- BƯỚC 8: KÝ SỐ MẬT MÃ ACCESS TOKEN JWT ---
	// Thực hiện ký số tạo JWT chứa các claims bảo mật để phân quyền cho Admin.
	// LƯU Ý QUAN TRỌNG: TokenUse đã được cập nhật chính xác từ "admin_api_token" sang "admin_api" theo đặc tả thiết kế mới.
	adminAPIToken, signErr := security.Sign(ctx, s.secrets, security.SecretFamilyAdminAPIKey, security.Claims{
		Subject:   "admin",
		AccessKey: deviceID,
		TokenID:   adminJTI.String(),
		TokenUse:  "admin_api",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.cfg.Security.AdminSessionTTL).Unix(),
	})
	if signErr != nil {
		_ = s.deviceRT.DeleteDeviceSecret(ctx, deviceID)
		loginOutcome = iamTaxonomy.AdminLoginOutcomeSignTokenError
		if errors.Is(signErr, security.ErrEmptySecret) {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeAuthUnavailable
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, signErr, iamTaxonomy.AdminLoginOutcomeAuthUnavailable)
		}
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, signErr, iamTaxonomy.AdminLoginOutcomeSignTokenError)
	}

	// --- BƯỚC 9: TRẢ VỀ KẾT QUẢ ĐĂNG NHẬP THÀNH CÔNG ---
	// Trả về AdminLoginResult chứa JWT, access_key (DeviceID), access_secret (DeviceSecret) của phiên và thời gian hết hạn.
	return iamEntity.AdminLoginResult{
		AdminAPIToken:            adminAPIToken,
		DeviceID:                 deviceID,
		DeviceSecret:             deviceSecret,
		ClientDeviceID:           clientDeviceID,
		ClientDeviceIDProvenance: string(clientDeviceProvenance),
		ExpiresAt:                now.Add(s.cfg.Security.AdminSessionTTL),
	}, nil
}

// RefreshAdminSession cấp lại admin_api_token cho device đang active mà không
// yêu cầu login lại bằng API key + MFA.
//
// Flow:
//  1. validate device_id,
//  2. load runtime fragment + CAS touch để gia hạn TTL theo phiên bản hiện tại,
//  3. ký lại admin_api_token mới với JTI mới.
//
// Boundary:
//   - Không reset device_secret để tránh phá vỡ HMAC từ phía client.
//   - Không sinh device_id mới; reuse device_id của runtime hiện tại.
//   - Mọi lỗi cache/sign quy về ErrAuthenticationUnavailable hoặc
//     ErrAdminLoginTokenIssueFailed để handler map HTTP nhất quán.
func (s *AdminAPIKeyService) RefreshAdminSession(ctx context.Context, deviceID string, ip *string, userAgent *string) (iamEntity.AdminLoginResult, error) {
	startedAt := time.Now()
	refreshOutcome := iamTaxonomy.OutcomeSuccess
	defer func() {
		var observeErr error
		if refreshOutcome != iamTaxonomy.OutcomeSuccess {
			observeErr = errors.New(refreshOutcome)
		}
		iamMetrics.ObserveAdminRefreshOutcome(refreshOutcome)
		iamMetrics.ObserveAdminRefreshLatency(time.Since(startedAt), observeErr)
	}()

	trimmedDeviceID := strings.TrimSpace(deviceID)
	if trimmedDeviceID == "" {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeInvalidArgument
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, iamTaxonomy.AdminRefreshOutcomeInvalidArgument)
	}
	runtimeRecord, runtimeErr := s.deviceRT.GetDeviceRuntime(ctx, trimmedDeviceID)
	if runtimeErr != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeLoadRuntimeErr
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, runtimeErr, iamTaxonomy.AdminRefreshOutcomeLoadRuntimeErr)
	}
	if runtimeRecord == nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeRuntimeNotFound
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, iamTaxonomy.AdminRefreshOutcomeRuntimeNotFound)
	}
	casOK, casErr := s.deviceRT.CompareAndTouchDeviceRuntime(ctx, trimmedDeviceID, runtimeRecord.Version, s.cfg.Security.AdminSessionTTL, ip, userAgent)
	if casErr != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeTouchRuntimeErr
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, casErr, iamTaxonomy.AdminRefreshOutcomeTouchRuntimeErr)
	}
	if !casOK {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeRuntimeConflict
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, iamTaxonomy.AdminRefreshOutcomeRuntimeConflict)
	}
	if s.secrets == nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeAuthUnavailable
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, nil, iamTaxonomy.AdminRefreshOutcomeAuthUnavailable)
	}

	now := time.Now().UTC()
	adminJTI, jtiErr := uuid.NewV7()
	if jtiErr != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeJTIIssueError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, jtiErr, iamTaxonomy.AdminRefreshOutcomeJTIIssueError)
	}
	adminAPIToken, signErr := security.Sign(ctx, s.secrets, security.SecretFamilyAdminAPIKey, security.Claims{
		Subject:   "admin",
		AccessKey: trimmedDeviceID,
		TokenID:   adminJTI.String(),
		TokenUse:  "admin_api_token",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.cfg.Security.AdminSessionTTL).Unix(),
	})
	if signErr != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeSignTokenError
		if errors.Is(signErr, security.ErrEmptySecret) {
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, signErr, iamTaxonomy.AdminRefreshOutcomeAuthUnavailable)
		}
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, signErr, iamTaxonomy.AdminRefreshOutcomeSignTokenError)
	}

	return iamEntity.AdminLoginResult{
		AdminAPIToken: adminAPIToken,
		DeviceID:      trimmedDeviceID,
		ExpiresAt:     now.Add(s.cfg.Security.AdminSessionTTL),
	}, nil
}

// formatRecoveryCodesForTelegram render danh sách recovery code thành block
// HTML có đánh số, an toàn cho Telegram parse_mode=HTML.
func formatRecoveryCodesForTelegram(codes []string) string {
	if len(codes) == 0 {
		return "<i>(none)</i>"
	}
	formatted := make([]string, 0, len(codes))
	for index, code := range codes {
		formatted = append(formatted, fmt.Sprintf("%d) <code>%s</code>", index+1, code))
	}
	return strings.Join(formatted, "\n")
}

// AdminLogout cleanup runtime fragment state theo device_id (nếu có).
// Handler sẽ chịu trách nhiệm clear cookie phía client.
func (s *AdminAPIKeyService) AdminLogout(ctx context.Context, deviceID string, ip *string, userAgent *string) error {
	trimmedDeviceID := strings.TrimSpace(deviceID)
	if trimmedDeviceID == "" {
		return nil
	}
	runtimeRecord, runtimeErr := s.deviceRT.GetDeviceRuntime(ctx, trimmedDeviceID)
	if runtimeErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, runtimeErr, "logout_cache_error")
	}
	if runtimeRecord != nil && strings.TrimSpace(runtimeRecord.TrackedDeviceID) != "" {
		runtimeIP := strings.TrimSpace(runtimeRecord.LastSeenIP)
		runtimeUA := strings.TrimSpace(runtimeRecord.LastSeenUserAgent)
		if ip != nil {
			requestIP := strings.TrimSpace(*ip)
			if requestIP != "" && requestIP != runtimeIP {
				runtimeIP = requestIP
				runtimeRecord.LastSeenDirty = true
			}
		}
		if userAgent != nil {
			requestUA := strings.TrimSpace(*userAgent)
			if requestUA != "" && requestUA != runtimeUA {
				runtimeUA = requestUA
				runtimeRecord.LastSeenDirty = true
			}
		}
		// Chỉ flush DB khi runtime đánh dấu dirty để tránh write mỗi request.
		if runtimeRecord.LastSeenDirty {
			seenAtUnix := runtimeRecord.LastSeenAt
			if seenAtUnix <= 0 {
				seenAtUnix = time.Now().UTC().Unix()
			}
			if err := s.repo.TouchAdminDeviceLastSeen(ctx, runtimeRecord.TrackedDeviceID, optionalStringPointer(runtimeIP), optionalStringPointer(runtimeUA), time.Unix(seenAtUnix, 0).UTC()); err != nil {
				return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "logout_runtime_error")
			}
		}
	}
	if err := s.deviceRT.DeleteDeviceSecret(ctx, trimmedDeviceID); err != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "logout_cache_error")
	}
	return nil
}

// FinalizeInactiveSessions quét runtime cache, flush last_seen xuống DB cho
// các device không hoạt động trước inactiveBefore, sau đó xoá runtime.
//
// Mục tiêu:
// - giảm áp lực ghi DB so với việc flush mỗi request,
// - đảm bảo last_seen vẫn được persist khi session timeout.
//
// Boundary:
// - Chỉ flush DB khi runtime đánh dấu LastSeenDirty = true.
// - Lỗi flush DB hoặc xoá cache bị nuốt để vòng quét tiếp tục với device kế.
func (s *AdminAPIKeyService) FinalizeInactiveSessions(ctx context.Context, inactiveBefore time.Time, limit int) error {
	records, err := s.deviceRT.ScanDeviceRuntimes(ctx, limit)
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "finalize_dependency_error")
	}
	threshold := inactiveBefore.UTC().Unix()
	for _, record := range records {
		if record.LastSeenAt <= 0 || record.LastSeenAt > threshold {
			continue
		}
		// Finalize chỉ ghi last_seen nếu có delta đã track trong Redis runtime.
		if strings.TrimSpace(record.TrackedDeviceID) != "" && record.LastSeenDirty {
			_ = s.repo.TouchAdminDeviceLastSeen(ctx, record.TrackedDeviceID, optionalStringPointer(record.LastSeenIP), optionalStringPointer(record.LastSeenUserAgent), time.Unix(record.LastSeenAt, 0).UTC())
		}
		_ = s.deviceRT.DeleteDeviceSecret(ctx, record.DeviceID)
	}
	return nil
}

// loadAdminTOTPSecret đọc TOTP secret active từ DB, decrypt và cache RAM 5 phút.
//
// Mục tiêu:
// - giảm số lần decrypt + call DB trong burst login,
// - vẫn fallback DB khi cache miss/expired.
func (s *AdminAPIKeyService) loadAdminTOTPSecret(ctx context.Context) (string, error) {
	settings, err := s.repo.GetAdmin2FASettings(ctx)
	if err != nil {
		return "", apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	if settings == nil || strings.TrimSpace(settings.SecretCiphertext) == "" {
		return "", apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, "mfa_invalid")
	}
	if s.apiKeyCache != nil {
		if secret, ok := s.apiKeyCache.GetTOTPSecret(settings.UpdatedAt); ok {
			return secret, nil
		}
	}

	secret, decErr := security.DecryptSecret(settings.SecretCiphertext)
	if decErr != nil {
		return "", apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, decErr, "dependency_error")
	}
	if s.apiKeyCache != nil {
		s.apiKeyCache.SetTOTPSecret(settings.UpdatedAt, secret, 5*time.Minute)
	}
	return secret, nil
}

// loadActiveAdminAPIKey đọc key active với chiến lược RAM cache + fallback DB.
//
// Chính sách cache:
// - TTL mặc định 5 phút,
// - nếu key hết hạn sớm hơn 5 phút thì TTL co lại theo expires_at,
// - không trả key đã expired.
func (s *AdminAPIKeyService) loadActiveAdminAPIKey(ctx context.Context) (*iamEntity.AdminAPIKey, error) {
	now := time.Now().UTC()

	if s.apiKeyCache != nil {
		if cached, ok := s.apiKeyCache.GetActiveAPIKey(now); ok {
			item := cached
			return &item, nil
		}
	}

	item, err := s.repo.GetActiveAdminAPIKey(ctx)
	if err != nil || item == nil {
		return item, err
	}

	ttl := 5 * time.Minute
	if item.ExpiresAt.Before(now.Add(ttl)) {
		ttl = item.ExpiresAt.Sub(now)
	}
	if ttl <= 0 {
		return item, nil
	}

	if s.apiKeyCache != nil {
		s.apiKeyCache.SetActiveAPIKey(*item, ttl)
	}

	return item, nil
}

// joinCodes format danh sách recovery codes thành chuỗi nhiều dòng để notify.
func joinCodes(codes []string) string {
	if len(codes) == 0 {
		return ""
	}
	out := ""
	for i, code := range codes {
		if i > 0 {
			out += "\n"
		}
		out += code
	}
	return out
}

// optionalStringPointer trả *string khi raw có nội dung sau trim, ngược lại
// trả nil để repo phân biệt "không cập nhật" vs "set rỗng".
func optionalStringPointer(raw string) *string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	return &value
}

// normalizeDevicePublicKey decode + validate ed25519 public key (32 bytes) từ
// chuỗi base64 (std hoặc raw), sau đó re-encode về base64 std làm canonical
// form cho storage và so sánh fingerprint nhất quán.
func normalizeDevicePublicKey(raw string) (string, error) {
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
