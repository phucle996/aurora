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
//          Redis, được ký thành JWT ngắn hạn với loại secret `admin_api_key`.
//
//     2) MẶT PHẲNG NGHIỆP VỤ NGƯỜI DÙNG VÀ NỀN TẢNG (USER & PLATFORM PLANE):
//        * Do `AuthService` (xem auth_service.go) quản lý. Dành cho Customer và các tài khoản nền
//          tảng hệ thống (root level 0, sys_admin level 1, support level 2 — xem auth_service.go).
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Khóa API Key và thiết lập TOTP Secret được lưu trữ mã hóa và xác thực duy nhất trong
//     Postgres Database.
//   - Phiên Admin dùng mô hình **Fragment Token** (3 mảnh) thay vì 1 access token đơn thông thường:
//     Mảnh 1 — JWT access token ngắn hạn (stateless, ký bằng `admin_api_key`).
//     Mảnh 2 — `access_key`: định danh phiên, lưu trong Redis (`AdminAccessSessionCache`).
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
//     token với loại secret `admin_api_key`.
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
	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	iamCache "controlplane/internal/iam/cache"
	deviceHint "controlplane/internal/iam/devicehint"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	coreEntity "controlplane/internal/core/domain/entity"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	"controlplane/pkg/constant"
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
	sessionCache    iamCache.AdminAccessSessionCache
	apiKeyCache     iamCache.AdminAPIKeyCache
	l1Registry      *cacheengine.CacheRegistry
	rotationTrigger iamCache.AdminKeyRotationTriggerCache
}

// NewAdminAPIKeyService tạo service instance với các dependency cần thiết cho
// bootstrap/login/logout flow.
func NewAdminAPIKeyService(
	cfg *config.Config,
	repo iamRepoInterface.AdminAPIKeyRepository,
	telegram *telegram.TelegramClient,
	sessionCache iamCache.AdminAccessSessionCache,
	apiKeyCache iamCache.AdminAPIKeyCache,
	l1Registry *cacheengine.CacheRegistry,
	rotationTrigger ...iamCache.AdminKeyRotationTriggerCache,
) iamSvcInterface.AdminAPIKeyService {
	var trigger iamCache.AdminKeyRotationTriggerCache
	if len(rotationTrigger) > 0 {
		trigger = rotationTrigger[0]
	}
	svc := &AdminAPIKeyService{
		cfg:             cfg,
		repo:            repo,
		telegram:        telegram,
		sessionCache:    sessionCache,
		apiKeyCache:     apiKeyCache,
		l1Registry:      l1Registry,
		rotationTrigger: trigger,
	}

	return svc
}

// RotateAdminAPIKeyEmergency thực thi quy trình xoay vòng Admin API Key khẩn cấp.
// Quy trình này được kích hoạt khi phát hiện hoặc nghi ngờ hệ thống bị thỏa hiệp (compromise)
// hoặc được kích hoạt tự động theo chính sách của scheduler/operator.
//
// Callsite:
//   - Được gọi từ HTTP Handler (`admin_auth_handler.go`) thông qua endpoint `/admin/auth/rotate-key`
//     khi operator chủ động kích hoạt thủ công.
//   - Được gọi từ scheduler định kỳ thông qua `TryProcessAdminKeyRotationTrigger`.
//
// Notes:
//   - Quy trình này chạy đồng bộ và sử dụng cơ chế Distributed Advisory Lock ở DB để ngăn chặn race condition.
//   - Chế độ an toàn tuyệt đối: Khóa API Key nguyên bản (plaintext) chỉ tồn tại duy nhất trong RAM
//     và tin nhắn Telegram, không bao giờ được ghi xuống log hệ thống.
//   - Giao dịch có điều kiện: Nếu kênh thông báo Telegram lỗi, quy trình sẽ huỷ bỏ (abort) ngay lập tức
//     để đảm bảo không ghi khóa mới vào DB khi operator chưa nhận được.
func (s *AdminAPIKeyService) RotateAdminAPIKeyEmergency(ctx context.Context) error {
	// Bước 1: Acquire rotation lock toàn cục để chống thực thi đồng thời (race condition)
	lock, err := s.repo.AcquireRotationLock(ctx)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already held") {
			iamMetrics.ObserveServiceCall("admin_key_rotation", iamTaxonomy.AdminRotateLockBusy, "n/a")
			return apperr.Wrap(iamTaxonomy.ErrAdminRotationLockBusy, err)
		}
		iamMetrics.ObserveServiceCall("admin_key_rotation", iamTaxonomy.AdminRotateFail, "n/a")
		return apperr.Wrap(iamTaxonomy.ErrAdminRotationFailed, err)
	}
	defer lock.Release(ctx)

	// Bước 2: Sinh ngẫu nhiên API key mới (plaintext) + tính toán hash SHA256 và thời gian hết hạn
	plainKey, err := security.GenerateToken(48)
	if err != nil {
		iamMetrics.ObserveServiceCall("admin_key_rotation", iamTaxonomy.AdminRotateFail, "n/a")
		return apperr.Wrap(iamTaxonomy.ErrAdminRotationFailed, err)
	}
	expiresAt := time.Now().UTC().Add(s.cfg.Security.AdminAPITokenTTL)

	// Bước 3: Thông báo khóa plaintext qua kênh Telegram khẩn cấp an toàn trước khi lưu DB.
	// Nếu bước này lỗi, bắt buộc hủy bỏ (abort) để tránh mồ côi khóa trong database.
	msg := fmt.Sprintf("<b>ADMIN ROTATION SUCCESS</b>\nAPI Key: <code>%s</code>\nExpires: <code>%s</code>", plainKey, expiresAt.Format(time.RFC3339))
	if sendErr := s.telegram.SendMessage(msg); sendErr != nil {
		iamMetrics.ObserveServiceCall("admin_key_rotation", iamTaxonomy.AdminRotateDeliveryFail, "n/a")
		return apperr.Wrap(iamTaxonomy.ErrAdminRotationDelivery, sendErr)
	}

	// Bước 4: Tạo thực thể AdminAPIKey và lưu vào cơ sở dữ liệu ở trạng thái active kế tiếp
	newID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		iamMetrics.ObserveServiceCall("admin_key_rotation", iamTaxonomy.AdminRotateFail, "n/a")
		return apperr.Wrap(iamTaxonomy.ErrAdminRotationFailed, uuidErr)
	}

	actor := "admin-emergency-rotate"
	apiKeyEntity := iamEntity.AdminAPIKey{
		ID:        newID,
		KeyHash:   security.HashTokenSHA256(plainKey),
		CreatedBy: &actor,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	}

	if err := s.repo.PrepareNextAdminAPIKey(ctx, apiKeyEntity); err != nil {
		iamMetrics.ObserveServiceCall("admin_key_rotation", iamTaxonomy.AdminRotateFail, "n/a")
		return apperr.Wrap(iamTaxonomy.ErrAdminRotationFailed, err)
	}

	// Bước 5: Xóa cờ chỉ định xoay vòng khóa khẩn cấp (nếu có).
	_ = s.rotationTrigger.ClearRotationRequired(ctx)

	// Ghi nhận thành công thực tế của việc xoay vòng khóa
	iamMetrics.ObserveServiceCall("admin_key_rotation", iamTaxonomy.OutcomeSuccess, "n/a")
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
		return fmt.Errorf("failed to check rotation required: %w", err)
	}
	if !required {
		return nil
	}
	if err := s.RotateAdminAPIKeyEmergency(ctx); err != nil {
		return err
	}
	return nil
}

// Bootstrap khởi tạo Admin API Key và các thiết lập bảo mật đầu tiên cho toàn bộ hệ thống.
// Quy trình này thiết lập các khóa truy cập, cơ chế xác thực hai lớp (TOTP), và mã khôi phục khẩn cấp.
//
// Callsite:
//   - Được gọi một lần duy nhất khi hệ thống được dựng lên (Bootstrap/Provisioning stage)
//     thông qua CLI command hoặc HTTP Init API.
//
// Notes:
//   - Quy trình sử dụng cơ chế Bootstrap Advisory Lock để đảm bảo chỉ có duy nhất một tiến trình
//     thực thi khởi tạo thành công tại một thời điểm trên toàn cụm HA.
//   - Chế độ tự động hoàn trả (Rollback): Nếu hệ thống không thể thông báo thông tin bảo mật đầu tiên
//     đến Telegram SRE sau 3 lần thử lại (do Telegram sập hoặc ngắt mạng), DB sẽ được rollback sạch sẽ
//     để đảm bảo không tồn tại khóa truy cập mồ côi mà người vận hành không biết.
func (s *AdminAPIKeyService) Bootstrap(ctx context.Context, actor string) error {
	// Bước 1: Lock bootstrap toàn cục để đảm bảo chạy đơn chiếc duy nhất trên toàn cụm HA
	lock, err := s.repo.AcquireBootstrapLock(ctx)
	if err != nil {
		return fmt.Errorf("%w: failed to acquire bootstrap lock: %v", iamTaxonomy.ErrAdminBootstrapLockFailed, err)
	}
	defer lock.Release(ctx)

	// Bước 2: Kiểm tra tiền đề điều kiện (Precondition) - hệ thống phải chưa có active key nào
	now := time.Now().UTC()
	active, err := s.repo.GetActiveAdminAPIKey(ctx)
	if err != nil {
		return fmt.Errorf("%w: failed to load active admin API key: %v", iamTaxonomy.ErrAdminBootstrapPreconditionFailed, err)
	}
	if active != nil {
		return fmt.Errorf("%w: admin API key already bootstrapped", iamTaxonomy.ErrAdminBootstrapNotAllowed)
	}

	expiresAt := now.Add(s.cfg.Security.AdminAPITokenTTL)

	// Bước 3: Tạo ngẫu nhiên Admin API key nguyên bản (plaintext) và mã hóa SHA256 để lưu DB
	plainKey, err := security.GenerateToken(48)
	if err != nil {
		return fmt.Errorf("%w: failed to generate admin API key: %v", iamTaxonomy.ErrAdminBootstrapPersistFailed, err)
	}
	keyHash := security.HashTokenSHA256(plainKey)

	// Bước 4: Tạo cấu hình TOTP 2FA (MFA) mặc định và mã hóa khóa bí mật (secret key)
	totpResult, err := security.GenerateTOTP("controlplane-admin", "admin@system")
	if err != nil {
		return fmt.Errorf("%w: failed to generate TOTP: %v", iamTaxonomy.ErrAdminBootstrapPersistFailed, err)
	}
	secretCipher, err := security.EncryptSecret(totpResult.Secret)
	if err != nil {
		return fmt.Errorf("%w: failed to encrypt TOTP secret: %v", iamTaxonomy.ErrAdminBootstrapPersistFailed, err)
	}

	// Bước 5: Sinh 8 mã khôi phục khẩn cấp (One-time Recovery Codes) dùng để backup khi mất 2FA
	recoveryHashes := make([]string, 0, 8)
	recoveryPlains := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		code, genErr := security.GenerateRecoveryCode(24)
		if genErr != nil {
			return fmt.Errorf("%w: failed to generate recovery code: %v", iamTaxonomy.ErrAdminBootstrapPersistFailed, genErr)
		}
		recoveryPlains = append(recoveryPlains, code)
		recoveryHashes = append(recoveryHashes, security.HashRecoveryCode(code))
	}

	// Bước 6: Lưu tất cả dữ liệu khởi tạo đầu tiên vào Cơ sở dữ liệu thông qua Repository
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
		return fmt.Errorf("%w: failed to bootstrap admin API key repository: %v", iamTaxonomy.ErrAdminBootstrapPersistFailed, err)
	}

	// Bước 7: Thông báo toàn bộ thông tin bảo mật nguyên bản (API Key, TOTP, Recovery Codes) qua Telegram của SRE.
	// Có cơ chế Retry tự động 3 lần (với Backoff tăng dần) chống rung mạng.
	var formattedCodes string
	if len(recoveryPlains) == 0 {
		formattedCodes = "<i>(none)</i>"
	} else {
		formatted := make([]string, 0, len(recoveryPlains))
		for index, code := range recoveryPlains {
			formatted = append(formatted, fmt.Sprintf("%d) <code>%s</code>", index+1, code))
		}
		formattedCodes = strings.Join(formatted, "\n")
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
		formattedCodes,
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

	// Bước 8: Giao dịch có điều kiện hoàn trả (Rollback)
	// Nếu SRE không nhận được tin nhắn bảo mật đầu tiên, buộc phải xóa bỏ toàn bộ dữ liệu vừa bootstrap ở DB.
	if rbErr := s.repo.RollbackBootstrap(ctx, payload); rbErr != nil {
		return fmt.Errorf("%w: failed to rollback bootstrap after notification failure: %v (rollback error: %v)", iamTaxonomy.ErrAdminBootstrapRollbackFailed, notifyErr, rbErr)
	}
	return fmt.Errorf("%w: failed to notify bootstrap via Telegram: %v", iamTaxonomy.ErrAdminBootstrapNotifyFailed, notifyErr)
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
	defer func() { iamMetrics.ObserveServiceCall("admin_login", loginOutcome, "n/a") }()

	// --- BƯỚC 2: CHUẨN HÓA VÀ KIỂM TRA KHÓA CÔNG KHAI THIẾT BỊ (DEVICE PUBLIC KEY NORMALIZATION) ---
	// Chuẩn hóa chuỗi khóa công khai Ed25519 nhận được từ Client sang định dạng chuẩn thống nhất.
	var canonicalPublicKey string
	var keyErr error
	rawKey := strings.TrimSpace(req.DevicePublicKey)
	if rawKey == "" {
		keyErr = fmt.Errorf("empty key")
	} else {
		var decoded []byte
		decoded, keyErr = base64.StdEncoding.DecodeString(rawKey)
		if keyErr != nil {
			decoded, keyErr = base64.RawStdEncoding.DecodeString(rawKey)
		}
		if keyErr == nil {
			if len(decoded) != ed25519.PublicKeySize {
				keyErr = fmt.Errorf("invalid key size")
			} else {
				canonicalPublicKey = base64.StdEncoding.EncodeToString(decoded)
			}
		}
	}
	if keyErr != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidDevicePublicKey
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, keyErr, iamTaxonomy.AdminLoginOutcomeInvalidArgument)
	}

	// Ghi nhận mốc thời gian UTC hiện tại cho mọi giao dịch lưu trữ và xác thực
	now := time.Now().UTC()

	// --- BƯỚC 3: XÁC THỰC API KEY ADMIN (API KEY VERIFICATION) ---
	// Nạp API Key đang hoạt động của Admin từ bộ nhớ đệm 2 lớp (Cache-aside pattern: RAM/Redis hoặc DB).
	active, err := s.repo.GetActiveAdminAPIKey(ctx)
	if err != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.AdminLoginOutcomeSystemError)
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
	switch req.MFAMethod {
	case iamEntity.MFATypeTOTP:
		// Phân hệ TOTP: Nạp khóa bí mật 2FA của Admin từ cơ sở dữ liệu.
		var secret string
		settings, err := s.repo.GetAdmin2FASettings(ctx)
		if err != nil {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
		}
		if settings == nil || strings.TrimSpace(settings.SecretCiphertext) == "" {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidCredential
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, "mfa_invalid")
		}

		var cachedSecret string
		var cacheFound bool
		if s.apiKeyCache != nil {
			cachedSecret, cacheFound = s.apiKeyCache.GetTOTPSecret(settings.UpdatedAt)
		}

		if cacheFound {
			secret = cachedSecret
		} else {
			decSecret, decErr := security.DecryptSecret(settings.SecretCiphertext)
			if decErr != nil {
				loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
				return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, decErr, "dependency_error")
			}
			secret = decSecret
			if s.apiKeyCache != nil {
				s.apiKeyCache.SetTOTPSecret(settings.UpdatedAt, secret, 5*time.Minute)
			}
		}

		code := strings.TrimSpace(req.MFACode)
		secret = strings.TrimSpace(secret)

		// Ngăn chặn mã xác thực hoặc khóa bí mật trống.
		if code == "" {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidCredential
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, iamTaxonomy.AdminLoginOutcomeInvalidCredential)
		}
		if secret == "" {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidCredential
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, iamTaxonomy.AdminLoginOutcomeInvalidCredential)
		}

		// Thực hiện giải mã cryptographic secret và đối khớp mã TOTP với chu kỳ 30 giây và sai số lệch giờ cho phép (Skew) là 1 chu kỳ.
		okTOTP, totpErr := totp.ValidateCustom(code, secret, now, totp.ValidateOpts{
			Period:    30,
			Skew:      1,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if totpErr != nil {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidCredential
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, totpErr, iamTaxonomy.AdminLoginOutcomeInvalidCredential)
		}
		if !okTOTP {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidCredential
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, iamTaxonomy.AdminLoginOutcomeInvalidCredential)
		}

	case iamEntity.MFATypeRecoveryCode:
		// Phân hệ Recovery Code (Mã Khôi Phục): Thực hiện băm SHA256 mã khôi phục để đối khớp.
		codeHash := security.HashRecoveryCode(req.MFACode)

		// Để triệt tiêu hoàn toàn tấn công Race Condition (Double-Consume Attack),
		// hệ thống sử dụng Distributed Lock của Redis qua phương thức AcquireRecoveryConsumeLock.
		unlock, lockErr := s.apiKeyCache.AcquireRecoveryConsumeLock(ctx, codeHash, 5*time.Second)
		if lockErr != nil {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, lockErr, iamTaxonomy.AdminLoginOutcomeSystemError)
		}
		defer unlock()

		// Thực hiện tiêu hủy mã khôi phục trong Database (chỉ cho phép sử dụng duy nhất một lần).
		consumed, consumeErr := s.repo.ConsumeRecoveryCode(ctx, codeHash, now)
		if consumeErr != nil {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, consumeErr, iamTaxonomy.AdminLoginOutcomeSystemError)
		}
		if !consumed {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidCredential
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, iamTaxonomy.AdminLoginOutcomeInvalidCredential)
		}

	default:
		// Từ chối nếu phương thức MFA không thuộc các loại được hỗ trợ.
		loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidCredential
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, nil, iamTaxonomy.AdminLoginOutcomeInvalidCredential)
	}

	// --- BƯỚC 4.5: PHÂN GIẢI ZONE CODE SANG ZONE ID (ZONE CODE RESOLUTION) ---
	// SRE HA Warning: Thực hiện phân giải zone_code nhận được từ client để lấy zone_id (UUID).
	// Nếu zone_code là "global" thì gán rỗng (cho phép truy cập toàn cục). Ngược lại, tra cứu
	// thông qua L1 Cache Registry để tránh gây quá tải DB trong môi trường HA Cloud Native.
	var zoneIDStr string
	zoneCode := strings.ToLower(strings.TrimSpace(req.ZoneCode))
	if zoneCode == "" {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidArgument
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, fmt.Errorf("zone_code is empty"), iamTaxonomy.AdminLoginOutcomeInvalidArgument)
	}

	if !strings.EqualFold(zoneCode, "global") {
		// Gọi L1 cache registry để dịch zone_code thành zone_id
		val, err := s.l1Registry.GetOrLoad(ctx, "zone_by_code", zoneCode)
		if err != nil {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidArgument
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, fmt.Errorf("failed to resolve zone code: %w", err), iamTaxonomy.AdminLoginOutcomeInvalidArgument)
		}
		resolvedZoneID, ok := val.(string)
		if !ok || resolvedZoneID == "" {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidArgument
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, fmt.Errorf("invalid zone code resolved"), iamTaxonomy.AdminLoginOutcomeInvalidArgument)
		}

		// Đảm bảo resolvedZoneID là dạng UUID hợp lệ
		if _, parseErr := uuid.Parse(resolvedZoneID); parseErr != nil {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, fmt.Errorf("resolved invalid uuid zone: %w", parseErr), iamTaxonomy.AdminLoginOutcomeSystemError)
		}
		zoneIDStr = resolvedZoneID
	}

	// --- BƯỚC 5: TẠO CẶP ĐỊNH DANH PHIÊN CHẠY (RUNTIME SESSION CONFIGURATION) ---
	// Sinh mã UUIDv7 ngẫu nhiên cho Access Key để định danh phiên làm việc hiện tại (mapped vào DeviceID trong API contract).
	accessKeyUUID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, uuidErr, iamTaxonomy.AdminLoginOutcomeSystemError)
	}
	accessKey := accessKeyUUID.String()
	// Sinh khóa bí mật Access Secret ngẫu nhiên dài 48 bytes có tính mật mã bảo mật cao (mapped vào DeviceSecret trong API contract).
	accessSecret, genErr := security.GenerateToken(48)
	if genErr != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, genErr, iamTaxonomy.AdminLoginOutcomeSystemError)
	}

	// --- BƯỚC 6: ĐĂNG KÝ VÀ LIÊN KẾT THIẾT BỊ VẬT LÝ VỚI DATABASE ---
	// Tạo dấu vân tay (Fingerprint) của khóa công khai bằng hàm băm SHA256.
	fp := sha256.Sum256([]byte(canonicalPublicKey))
	fph := hex.EncodeToString(fp[:])

	// Phân giải tên thiết bị gợi ý từ Client (HostnameHint & HostnameAlias).
	deviceName := deviceHint.ResolveDeviceName(req.HostnameHint, req.HostnameAlias)
	// Phân giải định danh thiết bị từ client gửi lên:
	// - clientDeviceID: Chuỗi định danh duy nhất của thiết bị dưới dạng string (lấy từ Header/Cookie X-Client-Device-Id).
	// - clientDeviceProvenance: Nguồn gốc của mã định danh này, cho biết ID này là do client cung cấp ("client") hay do hệ thống tự sinh mới ("server-bootstrap").
	clientDeviceID, clientDeviceProvenance := deviceHint.ResolveClientDeviceID(req.ClientDeviceID)

	// Chuyển đổi mã chuỗi sang định dạng UUID (ID thiết bị Admin dùng làm khóa chính trong Postgres & tracking redis).
	// Nếu mã từ client không hợp lệ hoặc rỗng, hệ thống sẽ tự động sinh mã UUID mới (Self-healing) và đánh dấu nguồn gốc là "server-bootstrap".
	deviceID, parseErr := uuid.Parse(clientDeviceID)
	if parseErr != nil {
		deviceID = uuid.New()
		clientDeviceID = deviceID.String()
		clientDeviceProvenance = deviceHint.ProvenanceServerBootstrap
	}

	// Ghi nhận thiết bị vật lý của Admin xuống Postgres Database.
	deviceBinding, bindErr := s.repo.UpsertAdminDeviceBinding(ctx, iamEntity.AdminDeviceBindingInput{
		ID:                   deviceID,
		DeviceName:           deviceName,
		PublicKey:            canonicalPublicKey,
		PublicKeyFingerprint: fph,
		Now:                  now,
	})
	if bindErr != nil {
		if errors.Is(bindErr, iamTaxonomy.ErrAdminLoginDeviceRevoked) {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidCredential
			return iamEntity.AdminLoginResult{}, apperr.Wrap(bindErr, bindErr, iamTaxonomy.AdminLoginOutcomeInvalidCredential)
		}
		if errors.Is(bindErr, iamTaxonomy.ErrAdminLoginDeviceQuarantined) {
			loginOutcome = iamTaxonomy.AdminLoginOutcomeInvalidCredential
			return iamEntity.AdminLoginResult{}, apperr.Wrap(bindErr, bindErr, iamTaxonomy.AdminLoginOutcomeInvalidCredential)
		}
		loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginDeviceBindingFailed, bindErr, iamTaxonomy.AdminLoginOutcomeSystemError)
	}

	// --- BƯỚC 7: KÝ SỐ MẬT MÃ ACCESS TOKEN JWT (PRACTICAL FAIL-FAST) ---
	// Sinh Token ID duy nhất sử dụng UUIDv7 để phục vụ JTI Claim giúp định danh chính xác JWT.
	adminJTI, jtiErr := uuid.NewV7()
	if jtiErr != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, jtiErr, iamTaxonomy.AdminLoginOutcomeSystemError)
	}
	tokenJTI := adminJTI.String()

	// Ký admin_api token sử dụng CacheRegistry và coreEntity.RuntimeSecrets
	val, err := s.l1Registry.GetOrLoad(ctx, "admin_api_key", "")
	if err != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.AdminLoginOutcomeSystemError)
	}
	secrets, ok := val.(*coreEntity.RuntimeSecrets)
	if !ok || secrets == nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, errors.New("invalid runtime secrets type"), iamTaxonomy.AdminLoginOutcomeSystemError)
	}

	adminAPIToken, signErr := security.SignWithSecret(security.Claims{
		Subject:   "admin",
		AccessKey: accessKey,
		TokenID:   tokenJTI,
		TokenUse:  "admin_api",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.cfg.Security.AdminSessionTTL).Unix(),
		ZoneID:    zoneIDStr,
	}, secrets.Active.Secret)
	if signErr != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, signErr, iamTaxonomy.AdminLoginOutcomeSystemError)
	}

	// --- BƯỚC 8: THIẾT LẬP VÀ LIÊN KẾT PHIÊN LÀM VIỆC TRONG REDIS ---
	// Khởi tạo bản ghi Live Runtime Session hoàn chỉnh trong Redis Cache sau khi đã ký số thành công.
	if err := s.sessionCache.SetAccessSession(ctx, iamCache.AdminAccessSession{
		AccessKey:        accessKey,
		AccessSecretHash: security.HashTokenSHA256(accessSecret),
		TrackedDeviceID:  deviceBinding.ID.String(),
		DevicePublicKey:  canonicalPublicKey,
		TokenJTI:         tokenJTI,
		Version:          1,
		LastSeenAt:       now.UTC().Unix(),
	}, s.cfg.Security.AdminSessionTTL); err != nil {
		loginOutcome = iamTaxonomy.AdminLoginOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, err, iamTaxonomy.AdminLoginOutcomeSystemError)
	}

	// --- BƯỚC 9: TRẢ VỀ KẾT QUẢ ĐĂNG NHẬP THÀNH CÔNG ---
	// Trả về AdminLoginResult chứa JWT, Access Key, Access Secret của phiên và thời gian hết hạn.
	return iamEntity.AdminLoginResult{
		AdminAPIToken:            adminAPIToken,
		AccessKey:                accessKey,
		AccessSecret:             accessSecret,
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
func (s *AdminAPIKeyService) RefreshAdminSession(ctx context.Context, zoneCode string, ip *string, userAgent *string) (iamEntity.AdminLoginResult, error) {
	// --- KHỞI TẠO ĐO LƯỜNG TELEMETRY & METRICS ---
	startedAt := time.Now()
	refreshOutcome := iamTaxonomy.OutcomeSuccess
	defer func() {
		var observeErr error
		if refreshOutcome != iamTaxonomy.OutcomeSuccess {
			observeErr = errors.New(refreshOutcome)
		}
		iamMetrics.ObserveServiceCall("admin_refresh", refreshOutcome, "n/a")
		iamMetrics.ObserveDownstream("redis", "admin_refresh", time.Since(startedAt), observeErr)
	}()

	// --- BƯỚC 1: TRÍCH XUẤT ACCESS KEY TỪ GO CONTEXT ---
	// Trích xuất accessKey trực tiếp từ Go standard context thay vì nhận qua tham số
	accessKeyVal := ctx.Value(constant.ContextKeyAdminAccessKey)
	accessKey, ok := accessKeyVal.(string)
	if !ok || strings.TrimSpace(accessKey) == "" {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeInvalidArgument
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, refreshOutcome)
	}
	trimmedAccessKey := strings.TrimSpace(accessKey)

	// --- BƯỚC 2: PHÂN GIẢI MÃ PHÂN VÙNG THÀNH UUID QUA L1 CACHE REGISTRY ---
	trimmedZoneCode := strings.ToLower(strings.TrimSpace(zoneCode))
	var resolvedZoneID string
	if !strings.EqualFold(trimmedZoneCode, "global") {
		// Gọi L1 cache để phân giải zone_code -> zone_id (UUID)
		val, err := s.l1Registry.GetOrLoad(ctx, "zone_by_code", trimmedZoneCode)
		if err != nil {
			refreshOutcome = iamTaxonomy.AdminRefreshOutcomeSystemError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "zone_resolution_failed")
		}
		zoneIDStr, ok := val.(string)
		if !ok || zoneIDStr == "" {
			refreshOutcome = iamTaxonomy.AdminRefreshOutcomeInvalidArgument
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, fmt.Errorf("invalid zone ID resolved from code: %s", trimmedZoneCode), "zone_resolution_failed")
		}
		resolvedZoneID = zoneIDStr
	}

	// --- BƯỚC 3: TRUY VẤN VÀ KIỂM TRA PHIÊN LÀM VIỆC HIỆN TẠI TRONG REDIS ---
	runtimeRecord, runtimeErr := s.sessionCache.GetAccessSession(ctx, trimmedAccessKey)
	if runtimeErr != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, runtimeErr, iamTaxonomy.AdminRefreshOutcomeSystemError)
	}
	if runtimeRecord == nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeInvalidSession
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, refreshOutcome)
	}

	// --- BƯỚC 4: THỰC THI SO SÁNH & GIA HẠN PHIÊN (COMPARE-AND-SWAP / Touch) ---
	// Atomic CAS LUA Script trên Redis để kiểm chứng version và thiết lập trực tiếp TTL của phiên cũ về 10 giây.
	// Việc thiết lập trực tiếp 10 giây ở đây giúp tối ưu hóa hiệu năng, loại bỏ hoàn toàn 1 lần ghi/RTT dư thừa xuống Redis ở cuối luồng.
	casOK, casErr := s.sessionCache.CompareAndTouchAccessSession(ctx, trimmedAccessKey, runtimeRecord.Version, 10*time.Second, ip, userAgent)
	if casErr != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, casErr, iamTaxonomy.AdminRefreshOutcomeSystemError)
	}
	if !casOK {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeInvalidSession
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, iamTaxonomy.AdminRefreshOutcomeInvalidSession)
	}

	now := time.Now().UTC()

	// --- BƯỚC 5: SINH MỚI BỘ BA TRINITY CREDENTIALS (ACCESS KEY, SECRET, JTI) ---
	// Sinh mới access key (UUID v7)
	accessKeyNewUUID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, uuidErr, iamTaxonomy.AdminRefreshOutcomeSystemError)
	}
	accessKeyNew := accessKeyNewUUID.String()

	// Sinh mới access secret thô (48 bytes)
	accessSecretNew, genErr := security.GenerateToken(48)
	if genErr != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, genErr, iamTaxonomy.AdminRefreshOutcomeSystemError)
	}

	// Sinh mới token JTI (UUID v7)
	tokenJTINewUUID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, uuidErr, iamTaxonomy.AdminRefreshOutcomeSystemError)
	}

	// --- BƯỚC 7: THIẾT LẬP PHIÊN LÀM VIỆC MỚI VÀO REDIS CACHE ---
	if err := s.sessionCache.SetAccessSession(ctx, iamCache.AdminAccessSession{
		AccessKey:         accessKeyNew,
		AccessSecretHash:  security.HashTokenSHA256(accessSecretNew),
		TrackedDeviceID:   runtimeRecord.TrackedDeviceID,
		DevicePublicKey:   runtimeRecord.DevicePublicKey,
		TokenJTI:          tokenJTINewUUID.String(),
		Version:           1,
		LastSeenAt:        now.UTC().Unix(),
		LastSeenIP:        runtimeRecord.LastSeenIP,
		LastSeenUserAgent: runtimeRecord.LastSeenUserAgent,
	}, s.cfg.Security.AdminSessionTTL); err != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, err, iamTaxonomy.AdminRefreshOutcomeSystemError)
	}

	// --- BƯỚC 8: KÝ LẠI TOKEN JWT MỚI VỚI ZONEID MỤC TIÊU ---
	valNew, errNew := s.l1Registry.GetOrLoad(ctx, "admin_api_key", "")
	if errNew != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, errNew, iamTaxonomy.AdminRefreshOutcomeSystemError)
	}
	secretsNew, okNew := valNew.(*coreEntity.RuntimeSecrets)
	if !okNew || secretsNew == nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, errors.New("invalid runtime secrets type"), iamTaxonomy.AdminRefreshOutcomeSystemError)
	}

	adminAPITokenNew, signErr := security.SignWithSecret(security.Claims{
		Subject:   "admin",
		AccessKey: accessKeyNew,
		TokenID:   tokenJTINewUUID.String(),
		TokenUse:  "admin_api",
		ZoneID:    resolvedZoneID,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.cfg.Security.AdminSessionTTL).Unix(),
	}, secretsNew.Active.Secret)
	if signErr != nil {
		refreshOutcome = iamTaxonomy.AdminRefreshOutcomeSystemError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginTokenIssueFailed, signErr, iamTaxonomy.AdminRefreshOutcomeSystemError)
	}

	// SRE Note: Ở thiết kế cũ, bước này cần gọi TouchAccessSession để giảm TTL về 10 giây.
	// Hiện tại, bước này đã được thực thi tối ưu nguyên tử trực tiếp từ Bước 4 (CAS LUA Script)
	// nên không cần gửi thêm lệnh RTT nào xuống Redis nữa.

	return iamEntity.AdminLoginResult{
		AdminAPIToken:  adminAPITokenNew,
		AccessKey:      accessKeyNew,
		AccessSecret:   accessSecretNew,
		ClientDeviceID: runtimeRecord.TrackedDeviceID,
		ExpiresAt:      now.Add(s.cfg.Security.AdminSessionTTL),
	}, nil
}

// AdminLogout thực hiện phế bỏ phiên làm việc của Admin ngay lập tức bằng cách xóa access key khỏi Redis Cache.
// Để bảo toàn thông tin thiết bị cuối cùng mà không làm ảnh hưởng đến thời gian phản hồi (latency),
// việc cập nhật thông tin thiết bị xuống Postgres Database được thực hiện bất đồng bộ (Asynchronous Background Flush)
// dưới cơ chế bảo vệ cắt tải chủ động (Load Shedding) sử dụng context timeout 1 giây và chỉ ghi khi thực sự thay đổi (LastSeenDirty = true).
func (s *AdminAPIKeyService) AdminLogout(ctx context.Context, accessKey string, ip *string, userAgent *string) error {
	// 1. Đọc nhanh thông tin session hiện tại trong Redis để lấy dữ liệu thiết bị (nếu có).
	runtimeRecord, runtimeErr := s.sessionCache.GetAccessSession(ctx, accessKey)
	if runtimeErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, runtimeErr, iamTaxonomy.AdminLogoutOutcomeSystemError)
	}

	// 2. PHẾ BỎ PHIÊN LÀM VIỆC LẬP TỨC (SECURITY EXTREMELY CRITICAL)
	// Thực hiện xóa session khỏi Redis trước để đảm bảo phiên chạy bị vô hiệu hóa ngay lập tức.
	if err := s.sessionCache.DeleteAccessSession(ctx, accessKey); err != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, iamTaxonomy.AdminLogoutOutcomeSystemError)
	}

	// 3. CẬP NHẬT TRẠNG THÁI THIẾT BỊ BẤT ĐỒNG BỘ (ASYNCHRONOUS DB FLUSH)
	// Đẩy tác vụ ghi DB xuống một background goroutine để không block luồng phản hồi chính (Latency < 1ms).
	if runtimeRecord != nil && strings.TrimSpace(runtimeRecord.TrackedDeviceID) != "" {
		go func() {
			// Tạo một context chạy nền tách biệt hoàn toàn với timeout chặt chẽ (1s) để tránh treo khi DB chậm/quá tải.
			bgCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			runtimeIP := strings.TrimSpace(runtimeRecord.LastSeenIP)
			runtimeUA := strings.TrimSpace(runtimeRecord.LastSeenUserAgent)
			dirty := runtimeRecord.LastSeenDirty

			if ip != nil {
				requestIP := strings.TrimSpace(*ip)
				if requestIP != "" && requestIP != runtimeIP {
					runtimeIP = requestIP
					dirty = true
				}
			}
			if userAgent != nil {
				requestUA := strings.TrimSpace(*userAgent)
				if requestUA != "" && requestUA != runtimeUA {
					runtimeUA = requestUA
					dirty = true
				}
			}

			if dirty {
				seenAtUnix := runtimeRecord.LastSeenAt
				if seenAtUnix <= 0 {
					seenAtUnix = time.Now().UTC().Unix()
				}
				// Nuốt mọi lỗi ở luồng nền vì tác vụ đăng xuất chính đã hoàn thành thành công và để bảo vệ DB.
				_ = s.repo.TouchAdminDeviceLastSeen(bgCtx, runtimeRecord.TrackedDeviceID, optionalStringPointer(runtimeIP), optionalStringPointer(runtimeUA), time.Unix(seenAtUnix, 0).UTC())
			}
		}()
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
	records, err := s.sessionCache.ScanAccessSessions(ctx, limit)
	if err != nil {
		return err
	}
	threshold := inactiveBefore.UTC().Unix()
	for _, record := range records {
		if record.LastSeenAt <= 0 || record.LastSeenAt > threshold {
			continue
		}
		// Finalize chỉ ghi last_seen nếu có delta đã track trong Redis runtime.
		if strings.TrimSpace(record.TrackedDeviceID) != "" && record.LastSeenDirty {
			_ = s.repo.TouchAdminDeviceLastSeen(ctx, record.TrackedDeviceID, optionalStringPointer(record.LastSeenIP),
				optionalStringPointer(record.LastSeenUserAgent), time.Unix(record.LastSeenAt, 0).UTC())
		}
		_ = s.sessionCache.DeleteAccessSession(ctx, record.AccessKey)
	}
	return nil
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
