// ======================================================================================================
// 📂 MODULE: controlplane/internal/iam/service/admin_api_key_service.go
//            Đặc Tả Nghiệp Vụ Quản Trị Hệ Thống & Xác Thực Admin
// ======================================================================================================
//
// [INFO] service này cung cấp xác thực cho quản trị hạ tầng thông qua admin UI
// [INFO] admin key này không phải là 1 user mà là 1 method để access vào controlplane
// [INFO] admin UI do SRE dùng => để access vào controlplane
//
// Controlplane chia thành 2 mặt phẳng: Mặt phẳng quản trị hệ thống & mặt phẳng người dùng
//
// Mặt phẳng quản trị hệ thống do AdminAPIKeyService quản lý toàn bộ vòng đời của Admin:
// - Bootstrap → Login → Session Refresh → Logout → Emergency Key Rotation
//
// Phiên Admin dùng mô hình **Trinity Token** (3 mảnh) thay vì 1 access token đơn thông thường:
//   - Mảnh 1 — JWT access token ngắn hạn (stateless, ký bằng `admin_api_key`).
//   - Mảnh 2 — `access_key`: định danh phiên, lưu trong Redis (`AdminAccessSessionCache`).
//   - Mảnh 3 — `access_secret`: bí mật phiên, chỉ lưu dạng hash trong Redis.
//   Middleware phải xác thực cả 3 mảnh khớp nhau thì phiên mới được coi là hợp lệ.

// ======================================================================================================

package iamSvcImpl

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"controlplane/infra/telegram"
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
	"controlplane/pkg/constant"
	"errors"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

type AdminAPIKeyService struct {
	cfg         *config.Config
	repo        iamRepoInterface.AdminAPIKeyRepository
	telegram    *telegram.TelegramClient
	cacheEngine *cacheengine.CacheRegistry
}

func NewAdminAPIKeyService(
	cfg *config.Config,
	repo iamRepoInterface.AdminAPIKeyRepository,
	telegram *telegram.TelegramClient,
	cacheEngine *cacheengine.CacheRegistry,
) iamSvcInterface.AdminAPIKeyService {
	return &AdminAPIKeyService{
		cfg:         cfg,
		repo:        repo,
		telegram:    telegram,
		cacheEngine: cacheEngine,
	}
}

// RotateAdminAPIKeyEmergency thực thi quy trình xoay vòng Admin API Key khẩn cấp.
// Quy trình này được kích hoạt khi phát hiện hoặc nghi ngờ hệ thống bị thỏa hiệp (compromise)
// hoặc được kích hoạt tự động theo chính sách của scheduler/operator.
func (s *AdminAPIKeyService) RotateAdminAPIKeyEmergency(ctx context.Context) error {

	// dùng wrap của apperr để wrap lỗi kèm outcome - nếu có outcome để map log ở handler với metrics label
	// nếu không có outcome thì trả err như bình thường

	const workflow = "admin_key_rotation"

	var outcome = iamTaxonomy.Success
	defer func() { iamMetrics.ServiceCall(workflow, outcome) }()

	// ==========================================================================
	// BƯỚC 1: ACQUIRE ROTATION LOCK TOÀN CỤC ĐỂ CHỐNG THỰC THI ĐỒNG THỜI (RACE CONDITION)
	// ==========================================================================
	lockStart := time.Now()

	lock, err := s.repo.AcquireRotationLock(ctx)
	if err != nil {
		// đo metrics số lần call bị lock chặn
		if errors.Is(err, iamTaxonomy.ErrLockAlreadyHeld) {
			iamMetrics.Downstream("repo", workflow, "AcquireRotationLock", iamTaxonomy.LockBusy, time.Since(lockStart), err)
			// Lock bận tức là tiền đề không thỏa mãn cho lượt xoay tiếp theo
			outcome = iamTaxonomy.LockBusy
			return apperr.Wrap(iamTaxonomy.ErrPreconditionFailed, err, outcome)
		}

		// Lỗi lock không mong đợi -> trả lỗi 500 ErrInternalError
		iamMetrics.Downstream("repo", workflow, "AcquireRotationLock", iamTaxonomy.LockUnknownError, time.Since(lockStart), err)
		outcome = iamTaxonomy.LockUnknownError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, outcome)
	}
	// luôn release lock sau khi hoàn thành hoặc xảy ra lỗi để tránh deadlock
	defer lock.Release(ctx)
	// lock thành công => đo metrics
	iamMetrics.Downstream("repo", workflow, "AcquireRotationLock", iamTaxonomy.LockRelease, time.Since(lockStart), nil)

	// ==========================================================================
	// BƯỚC 2: SINH NGẪU NHIÊN API KEY MỚI (PLAINTEXT)
	// ==========================================================================

	plainKey, err := security.GenerateToken(48)
	if err != nil {
		// đo metrics số lần sinh token thất bại
		outcome = iamTaxonomy.TokenGenerateFail
		// Sinh token ngẫu nhiên thất bại là lỗi phát hành token nội bộ -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, outcome)
	}

	// ==========================================================================
	// BƯỚC 3: THÔNG BÁO KHẨN CẤP KEY MỚI QUA TELEGRAM
	//   - Nếu bước này lỗi, bắt buộc hủy bỏ (abort) để tránh mồ côi khóa trong database.
	// ==========================================================================

	// tạo message thông báo key mới
	msg := fmt.Sprintf("<b>ADMIN ROTATION SUCCESS</b>\nAPI Key: <code>%s</code>\nExpires: <code>%s</code>",
		plainKey,
		time.Now().UTC().Add(s.cfg.Security.AdminAPITokenTTL).Format(time.RFC3339))

	// send message thông báo key mới qua telegram
	// đo metrics số lần gửi telegram thất bại
	telegramStart := time.Now()
	if sendErr := s.telegram.SendMessage(msg); sendErr != nil {
		iamMetrics.Downstream("telegram", workflow, "SendMessage", iamTaxonomy.TelegramSendFail, time.Since(telegramStart), sendErr)
		// Telegram thông báo lỗi là sự cố hạ tầng kết nối -> ErrInternalError
		outcome = iamTaxonomy.TelegramSendFail
		return apperr.Wrap(iamTaxonomy.ErrInternalError, sendErr, outcome)
	}

	// ==========================================================================
	// BƯỚC 4: TẠO THỰC THỂ ADMINAPIKEY VÀ LƯU VÀO CƠ SỞ DỮ LIỆU Ở TRẠNG THÁI ACTIVE KẾ TIẾP
	// ==========================================================================
	newID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		// đo metrics số lần sinh uuid thất bại
		outcome = iamTaxonomy.UuidGenerateFail
		// UUID sinh ra thất bại trả lỗi ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, uuidErr, outcome)
	}

	// tạo thực thể adminApiKey
	apiKeyEntity := iamEntity.AdminAPIKey{
		ID:        newID,
		KeyHash:   security.HashTokenSHA256(plainKey),
		CreatedBy: "SRE",
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(s.cfg.Security.AdminAPITokenTTL),
	}

	// call repo để save api key mới
	// đo metrics số lần call repo thất bại
	start := time.Now()
	if err := s.repo.PrepareNextAdminAPIKey(ctx, apiKeyEntity); err != nil {
		iamMetrics.Downstream("repo", workflow, "PrepareNextAdminAPIKey", iamTaxonomy.Failure, time.Since(start), err)
		// Database Prepare API Key lỗi là lỗi kết nối hệ thống -> ErrInternalError
		outcome = iamTaxonomy.Failure
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, outcome)
	}

	// Bước 5: Xóa cờ chỉ định xoay vòng khóa khẩn cấp (nếu có).
	// use defer để đảm bảo luôn xóa cờ dù có lỗi xảy ra sau này
	err = s.cacheEngine.L2.Delete(ctx, "iam:admin_key_rotation:required")
	if err != nil {
		// đo metrics số lần xóa cờ thất bại
		iamMetrics.Downstream("cache-engine-l2-delete", workflow, "DeleteRotationRequired", iamTaxonomy.Failure, time.Since(start), err)
		// Lỗi Redis Delete trả ErrInternalError
		outcome = iamTaxonomy.Failure
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, outcome)
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

	workflow := "admin-key-rotation-trigger"
	var outcome = iamTaxonomy.Success
	var runMetric bool
	defer func() {
		if runMetric {
			iamMetrics.ServiceCall(workflow, outcome)
		}
	}()

	// ==========================================================
	// BƯỚC 1: CHECK TRIGGER XOAY VÒNG KEY ADMIN ĐỂ ĐẢM BẢO HA VÀ CHỈ ROTATE KHI CÓ YÊU CẦU
	// ==========================================================
	l2Start := time.Now()
	_, _, required, err := s.cacheEngine.L2.Get(ctx, "iam:admin_key_rotation:required")
	if err != nil {
		runMetric = true
		outcome = iamTaxonomy.Failure
		iamMetrics.Downstream("cache-engine-l2-get", workflow, "GetRotationRequired", iamTaxonomy.Failure, time.Since(l2Start), err)
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, outcome)
	}

	// nếu không có trigger -> return
	if !required {
		return nil
	}

	// ==========================================================
	// BƯỚC 2: THỰC HIỆN ROTATE ADMIN API KEY KHẨN CẤP
	// ==========================================================
	// Thực hiện gọi service xoay vòng khóa khẩn cấp.
	// NOTE: Không đo lường metrics downstream ở đây vì:
	// 1. RotateAdminAPIKeyEmergency là một service method nội bộ của IAM module,
	//    không phải là cuộc gọi downstream sang database hay hệ thống ngoài.
	// 2. Bản thân RotateAdminAPIKeyEmergency đã tự phát emit metrics ServiceCall.
	//    Việc đo downstream ở đây dẫn đến hiện tượng double-emission (ghi nhận 2 lần)
	//    và phân loại sai ranh giới hạ tầng (gán nhãn kind = "repo").
	runMetric = true
	if err := s.RotateAdminAPIKeyEmergency(ctx); err != nil {
		outcome = iamTaxonomy.Failure
		if appErr, ok := apperr.As(err); ok {
			outcome = appErr.Outcome
		}
		return err
	}

	return nil
}

// Bootstrap khởi tạo Admin API Key và các thiết lập bảo mật đầu tiên cho toàn bộ hệ thống.
// Quy trình này thiết lập các khóa truy cập, cơ chế xác thực hai lớp (TOTP), và mã khôi phục khẩn cấp.
func (s *AdminAPIKeyService) Bootstrap(ctx context.Context) error {
	workflow := "admin-key-bootstrap"
	var outcome = iamTaxonomy.Success
	defer func() {
		iamMetrics.ServiceCall(workflow, outcome)
	}()

	// ==========================================================
	// BƯỚC 1: KHỞI TẠO CỜ LOCK ĐỂ ĐẢM BẢO CHẠY ĐƠN CHIẾC DUY NHẤT
	// ==========================================================
	lockStart := time.Now()
	lock, err := s.repo.AcquireBootstrapLock(ctx)
	if err != nil {
		outcome = iamTaxonomy.Failure
		// lock thất bại => đo metrics lỗi
		iamMetrics.Downstream("repo-lock", workflow, "AcquireBootstrapLock", iamTaxonomy.Failure, time.Since(lockStart), err)
		if errors.Is(err, iamTaxonomy.ErrLockAlreadyHeld) {
			// Lock bận tức là có tiến trình bootstrap song song khác đang chạy
			return apperr.Wrap(iamTaxonomy.ErrLockAlreadyHeld, err, iamTaxonomy.Failure)
		}
		// Lỗi kết nối DB khi lấy lock -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.Failure)
	}
	defer lock.Release(ctx)
	// lock thành công => đo metrics
	iamMetrics.Downstream("repo-lock", workflow, "AcquireBootstrapLock", iamTaxonomy.Success, time.Since(lockStart), nil)

	// ==========================================================
	// BƯỚC 2: KIỂM TRA TIỀN ĐỀ ĐIỀU KIỆN (PRECONDITION)
	// - Đảm bảo không có active key nào tồn tại trong DB trước khi bootstrap
	// - Nếu có => lỗi không cho phép bootstrap
	// ==========================================================
	precondStart := time.Now()
	active, err := s.repo.GetActiveAdminAPIKey(ctx)
	if err != nil {
		outcome = iamTaxonomy.Failure
		// nếu repo lỗi => trả về lỗi và ghi metrics downstream thất bại
		iamMetrics.Downstream("repo", workflow, "GetActiveAdminAPIKey", iamTaxonomy.Failure, time.Since(precondStart), err)
		// Lỗi truy vấn database là lỗi kết nối hệ thống -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.Failure)
	}
	// nếu active không nil => đã có key => trả về lỗi và ghi metrics downstream thất bại
	if active != nil {
		outcome = iamTaxonomy.PreConditionFailed
		// đo metrics tiền đề đã có active key
		iamMetrics.Downstream("repo", workflow, "GetActiveAdminAPIKey", iamTaxonomy.PreConditionFailed, time.Since(precondStart), iamTaxonomy.ErrActionNotAllowed)
		// Hệ thống đã được khởi tạo từ trước, không được phép bootstrap lại
		return apperr.Wrap(iamTaxonomy.ErrPreconditionFailed, errors.New("admin API key already exists in system"), iamTaxonomy.PreConditionFailed)
	}
	// hoàn tất pre-condition thành công => ghi metrics
	iamMetrics.Downstream("repo", workflow, "GetActiveAdminAPIKey", iamTaxonomy.PreConditionSuccess, time.Since(precondStart), nil)

	// ==========================================================
	// BƯỚC 3: TẠO ADMIN API KEY VÀ DỮ LIỆU CẦN THIẾT
	// ==========================================================

	// Tạo ngẫu nhiên Admin API key nguyên bản (plaintext) và mã hóa SHA256 để lưu DB
	plainKey, err := security.GenerateToken(48)
	if err != nil {
		outcome = iamTaxonomy.TokenGenerateFail
		// Sinh token ngẫu nhiên thất bại là lỗi phát hành token nội bộ -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.TokenGenerateFail)
	}

	// hash token nahanh inmemory nên không cần metrics
	keyHash := security.HashTokenSHA256(plainKey)

	// ==========================================================
	// BƯỚC 4: TẠO TOTP 2FA
	// ==========================================================

	// admin gen otp ít và khả năng lỗi gần như không có => không cần metrics.
	// nếu lỗi luôn thì app sẽ báo lỗi và không thể bootstrap được => và cần người can thiệp thủ công
	// trả lỗi để caller log ra là được
	totpResult, err := security.GenerateTOTP("controlplane-admin", "admin@system")
	if err != nil {
		outcome = iamTaxonomy.Failure
		// Sinh TOTP lỗi trả ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.Failure)
	}
	secretCipher, err := security.EncryptSecret(totpResult.Secret)
	if err != nil {
		outcome = iamTaxonomy.Failure
		// Mã hóa secret lỗi trả ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.Failure)
	}

	// ==========================================================
	// BƯỚC 5: TẠO RECOVERY CODES VÀ HASH
	// ==========================================================
	// admin gen recovery codes ít và khả năng lỗi gần như không có => không cần metrics.
	// nếu lỗi => trả lỗi để caller log ra => cần người can thiệp thủ công

	// tạo 8 recovery codes
	recoveryHashes := make([]string, 0, 8)
	recoveryPlains := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		code, genErr := security.GenerateRecoveryCode(24)
		if genErr != nil {
			outcome = iamTaxonomy.Failure
			// Sinh recovery code lỗi trả ErrInternalError
			return apperr.Wrap(iamTaxonomy.ErrInternalError, genErr, iamTaxonomy.Failure)
		}
		recoveryPlains = append(recoveryPlains, code)
		recoveryHashes = append(recoveryHashes, security.HashRecoveryCode(code))
	}

	// ==========================================================
	// BƯỚC 6: TẠO PAYLOAD
	// ==========================================================
	payload := iamEntity.AdminBootstrapPayload{
		Actor:              "SRE",
		KeyHash:            keyHash,
		ExpiresAt:          time.Now().Add(s.cfg.Security.AdminAPITokenTTL),
		RecoveryCodeHashes: recoveryHashes,
		GeneratedAt:        time.Now(),
		SecretCiphertext:   secretCipher,
	}

	start := time.Now()
	_, err = s.repo.Bootstrap(ctx, payload)
	if err != nil {
		outcome = iamTaxonomy.Failure
		// nếu repo lỗi => trả về lỗi và ghi metrics downstream thất bại
		iamMetrics.Downstream("repo", workflow, "Bootstrap", iamTaxonomy.Failure, time.Since(start), err)
		// Lỗi ghi DB khi bootstrap là lỗi kết nối hệ thống -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.Failure)
	}
	// nếu repo thành công => ghi metrics
	iamMetrics.Downstream("repo", workflow, "Bootstrap", iamTaxonomy.Success, time.Since(start), nil)

	// ==========================================================
	// BƯỚC 7: THÔNG BÁO TOÀN BỘ THÔNG TIN BẢO MẬT NGUYÊN BẢN
	// (API Key, TOTP, Recovery Codes) QUA TELEGRAM CỦA SRE.
	// Có cơ chế Retry tự động 3 lần (với Backoff tăng dần) chống rung mạng.
	// ==========================================================
	// Format recovery codes.
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

	// Format message.
	msg := fmt.Sprintf(
		"<b>✅ ADMIN BOOTSTRAP READY</b>\n\n"+
			"<b>🔐 API Key</b>\n<code>%s</code>\n\n"+
			"<b>⏰ Expires At (UTC)</b>\n<code>%s</code>\n\n"+
			"<b>🛡️ TOTP Secret</b>\n<code>%s</code>\n\n"+
			"<b>🧩 Recovery Codes (one-time)</b>\n%s\n\n"+
			"<i>Store these secrets in a secure vault. Do not share.</i>",
		plainKey,
		payload.ExpiresAt.Format(time.RFC3339),
		totpResult.Secret,
		formattedCodes,
	)

	// Retry tự động 3 lần (với Backoff tăng dần) chống rung mạng
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	var notifyErr error
	for i, wait := range backoff {
		if sendErr := s.telegram.SendMessage(msg); sendErr == nil {
			// nếu send success => return
			// ghi metrics
			iamMetrics.Downstream("telegram", workflow, "SendMessage", iamTaxonomy.Success, time.Since(start), nil)
			return nil
		} else {
			// nếu send fail => lưu lỗi
			notifyErr = sendErr
			// ghi metrics
			iamMetrics.Downstream("telegram", workflow, "SendMessage", iamTaxonomy.Failure, time.Since(start), notifyErr)
		}
		// nếu không phải lần cuối thì wait
		if i < len(backoff)-1 {
			time.Sleep(wait)
		}
	}

	// ==========================================================
	// BƯỚC 8: GIAO DỊCH CÓ ĐIỀU KIỆN HOÀN TRẢ (ROLLBACK)
	// Nếu SRE không nhận được tin nhắn bảo mật đầu tiên, buộc phải xóa bỏ toàn bộ dữ liệu vừa bootstrap ở DB.
	// ==========================================================
	if rbErr := s.repo.RollbackBootstrap(ctx, payload); rbErr != nil {
		outcome = iamTaxonomy.Failure
		// ghi metrics
		iamMetrics.Downstream("repo", workflow, "RollbackBootstrap", iamTaxonomy.Failure, time.Since(start), rbErr)
		// Lỗi rollback là lỗi kết nối hệ thống -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, rbErr, iamTaxonomy.Failure)
	}
	// ghi metrics
	iamMetrics.Downstream("repo", workflow, "RollbackBootstrap", iamTaxonomy.Success, time.Since(start), nil)
	outcome = iamTaxonomy.TelegramSendFail
	return nil
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

	workflow := "admin_login"
	loginOutcome := iamTaxonomy.Success
	defer func() { iamMetrics.ServiceCall(workflow, loginOutcome) }()

	// ========================================================
	//  BƯỚC 0: PHÒNG CHỐNG TRANH CHẤP ĐỒNG THỜI (CONCURRENCY LOCKING)
	// ========================================================

	startLock := time.Now()
	var lockKey string
	if req.ClientDeviceID != uuid.Nil {
		lockKey = "iam:admin_login_lock:device:" + req.ClientDeviceID.String()
	} else {
		lockKey = "iam:admin_login_lock:apikey:" + security.HashTokenSHA256(req.RawAPIKey)
	}

	ownerToken := uuid.New().String()
	lockScript := `
		if redis.call("set", KEYS[1], ARGV[1], "NX", "PX", ARGV[2]) then
			return 1
		else
			return 0
		end
	`
	res, err := s.cacheEngine.Exec.Execute(ctx, lockScript, []string{lockKey}, ownerToken, int64(5000))
	if err != nil {
		loginOutcome = iamTaxonomy.LockBusy
		iamMetrics.Downstream("l2-exec", workflow, "ExecuteLock", iamTaxonomy.LockBusy, time.Since(startLock), err)
		// Trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.LockBusy)
	}

	iamMetrics.Downstream("l2-exec", workflow, "ExecuteLock", iamTaxonomy.Success, time.Since(startLock), nil)
	if valInt, ok := res.(int64); !ok || valInt != 1 {
		loginOutcome = iamTaxonomy.LockBusy
		// Lock bận trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, errors.New("login lock already held for this device/key"), iamTaxonomy.LockBusy)
	}
	defer func() {
		unlockScript := `
			if redis.call("get", KEYS[1]) == ARGV[1] then
				return redis.call("del", KEYS[1])
			else
				return 0
			end
		`
		_, _ = s.cacheEngine.Exec.Execute(context.Background(), unlockScript, []string{lockKey}, ownerToken)
	}()

	// ========================================================
	//  BƯỚC 1: CHUẨN HÓA VÀ KIỂM TRA KHÓA CÔNG KHAI THIẾT BỊ (DEVICE PUBLIC KEY NORMALIZATION)
	// ========================================================
	// Chuẩn hóa chuỗi khóa công khai Ed25519 nhận được từ Client sang định dạng chuẩn thống nhất.
	var canonicalPublicKey string
	var keyErr error

	var decoded []byte
	// decode key theo base64 chuẩn
	decoded, keyErr = base64.StdEncoding.DecodeString(req.DevicePublicKey)
	if keyErr != nil {
		// nếu decode không thành công => decode theo raw base64
		decoded, keyErr = base64.RawStdEncoding.DecodeString(req.DevicePublicKey)
	}
	// nếu không lỗi => chuẩn hóa về dạng base64 encode
	if keyErr == nil {
		if len(decoded) != ed25519.PublicKeySize {
			keyErr = fmt.Errorf("invalid key size")
		} else {
			canonicalPublicKey = base64.StdEncoding.EncodeToString(decoded)
		}
	}

	if keyErr != nil {
		loginOutcome = iamTaxonomy.InvalidArgument
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, keyErr, iamTaxonomy.InvalidArgument)
	}

	/// ========================================================
	// BƯỚC 2: XÁC THỰC API KEY ADMIN (API KEY VERIFICATION)
	// ========================================================

	now := time.Now()
	val, err := s.cacheEngine.GetOrLoad(ctx, "admin_api_key_active", "")
	if err != nil {
		loginOutcome = iamTaxonomy.Failure
		iamMetrics.Downstream("cacheEngine", workflow, "GetActiveAdminAPIKey", iamTaxonomy.Failure, time.Since(now), err)
		// Trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("cacheEngine", workflow, "GetActiveAdminAPIKey", iamTaxonomy.Success, time.Since(now), nil)

	active, ok := val.(*iamEntity.AdminAPIKey)
	if !ok || active == nil {
		loginOutcome = iamTaxonomy.InvalidCredential
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidCredential, errors.New("api key not found"), iamTaxonomy.InvalidCredential)
	}
	// Thực hiện băm SHA256 API Key của Client gửi lên và đối khớp trực tiếp với KeyHash được cấu hình bảo mật.
	if active.KeyHash != security.HashTokenSHA256(req.RawAPIKey) {
		loginOutcome = iamTaxonomy.InvalidCredential
		iamMetrics.Downstream("repo", workflow, "HashTokenSHA256", iamTaxonomy.InvalidCredential, time.Since(now), nil)
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidCredential, errors.New("hash api key not match"), iamTaxonomy.InvalidCredential)
	}

	if req.ClientDeviceID != uuid.Nil {
		existingPubKey, err := s.repo.GetPublicKeyByDeviceID(ctx, req.ClientDeviceID.String())
		if err == nil {
			// Device ID đã tồn tại, kiểm tra sự trùng khớp của khóa công khai để tránh Device Hijacking
			if existingPubKey != canonicalPublicKey {
				loginOutcome = iamTaxonomy.Failure
				return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrDeviceBindingFailed, fmt.Errorf("device public key mismatch"), iamTaxonomy.Failure)
			}
		} else if !errors.Is(err, iamTaxonomy.ErrNotFound) {
			// Lỗi database thực sự trả ErrInternalError
			loginOutcome = iamTaxonomy.Failure
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.Failure)
		}
	}

	// ======================================================
	// BƯỚC 4: XÁC THỰC ĐA YẾU TỐ (MFA VALIDATION)
	// ======================================================
	switch req.MFAMethod {
	// nếu là totp => thực hiện xác thực totp
	case iamEntity.MFATypeTOTP:
		val, err := s.cacheEngine.GetOrLoad(ctx, "admin_2fa_secret", "")
		if err != nil {
			loginOutcome = iamTaxonomy.GetL1CacheFail
			// Trả ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.GetL1CacheFail)
		}

		// secret đã decrypted từ cache trước đó, do đó ta có thể dùng trực tiếp mà không cần trim space
		secret, ok := val.(string)
		if !ok {
			loginOutcome = iamTaxonomy.InvalidCredential
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidCredential, errors.New("2fa secret not found or empty"), iamTaxonomy.InvalidCredential)
		}

		// Thực hiện giải mã cryptographic secret và đối khớp mã TOTP với chu kỳ 30 giây và sai số lệch giờ cho phép (Skew) là 1 chu kỳ.
		okTOTP, totpErr := totp.ValidateCustom(req.MFACode, secret, now, totp.ValidateOpts{
			Period:    30,
			Skew:      1,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if totpErr != nil {
			loginOutcome = iamTaxonomy.Failure
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrMFAInvalid, totpErr, iamTaxonomy.Failure)
		}
		if !okTOTP {
			loginOutcome = iamTaxonomy.Failure
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrMFAInvalid, errors.New("totp passcode mismatch"), iamTaxonomy.Failure)
		}

		// Chống Replay TOTP bằng cơ chế Blacklist/Consume ngắn hạn trong Redis
		totpKey := "iam:admin_totp_consumed:" + strings.TrimSpace(req.MFACode)
		rdb := s.cacheEngine.L2.Client()
		setOk, setErr := rdb.SetNX(ctx, totpKey, "1", 90*time.Second).Result()
		if setErr != nil {
			loginOutcome = iamTaxonomy.Failure
			// Redis SetNX lỗi -> ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, setErr, iamTaxonomy.Failure)
		}
		if !setOk {
			loginOutcome = iamTaxonomy.Failure
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrMFAInvalid, errors.New("totp passcode already consumed"), iamTaxonomy.Failure)
		}

		// nếu là mã khôi phục => thực hiện xác thực mã khôi phục
	case iamEntity.MFATypeRecoveryCode:
		// Phân hệ Recovery Code (Mã Khôi Phục): Thực hiện băm SHA256 mã khôi phục để đối khớp.
		codeHash := security.HashRecoveryCode(req.MFACode)

		// Để triệt tiêu hoàn toàn tấn công Race Condition (Double-Consume Attack),
		// hệ thống sử dụng Distributed Lock của Redis qua L2 Lua Executor.
		// ownerToken dùng để xác thực khi unlock, đảm bảo chỉ có node nào lock thì mới có quyền unlock.
		// Đảm bảo đăng nhập 2 nơi cùng lúc , cùng dùng 1 recovery code không thể cùng consume được.
		ownerToken := uuid.New().String()
		lockKey := "iam:admin_recovery_consume_lock:" + strings.TrimSpace(codeHash)
		lockScript := `
			if redis.call("set", KEYS[1], ARGV[1], "NX", "PX", ARGV[2]) then
				return 1
			else
				return 0
			end
			`
		now := time.Now()
		res, err := s.cacheEngine.Exec.Execute(ctx, lockScript, []string{lockKey}, ownerToken, int64(5000))
		if err != nil {
			loginOutcome = iamTaxonomy.Failure
			iamMetrics.Downstream("cacheEngine", workflow, "Exec.Execute", iamTaxonomy.Failure, time.Since(now), err)
			// Redis Exec lock lỗi -> ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.Failure)
		}
		if valInt, ok := res.(int64); !ok || valInt != 1 {
			loginOutcome = iamTaxonomy.LockBusy
			iamMetrics.Downstream("cacheEngine", workflow, "Exec.Execute", iamTaxonomy.LockBusy, time.Since(now), nil)
			// Lock bận là lỗi xung đột concurrency hệ thống -> ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, errors.New("recovery consume lock already held"), iamTaxonomy.LockBusy)
		}
		defer func() {

			// luôn chạy unlock khi xong, không quan tâm tới lỗi
			unlockScript := `
			if redis.call("get", KEYS[1]) == ARGV[1] then
				return redis.call("del", KEYS[1])
			else
				return 0
			end
			`
			_, _ = s.cacheEngine.Exec.Execute(context.Background(), unlockScript, []string{lockKey}, ownerToken)
		}()

		// Thực hiện tiêu hủy mã khôi phục trong Database (chỉ cho phép sử dụng duy nhất một lần).
		now = time.Now()
		if consumeErr := s.repo.ConsumeRecoveryCode(ctx, codeHash, now); consumeErr != nil {
			loginOutcome = iamTaxonomy.Failure
			iamMetrics.Downstream("repo", workflow, "ConsumeRecoveryCode", iamTaxonomy.Failure, time.Since(now), consumeErr)
			if errors.Is(consumeErr, iamTaxonomy.ErrNotFound) || errors.Is(consumeErr, iamTaxonomy.ErrRecoveryCodeInvalid) {
				// Mã không tồn tại hoặc đã sử dụng trước đó
				return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrMFAInvalid, consumeErr, iamTaxonomy.Failure)
			}
			// Các lỗi DB khác trả ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, consumeErr, iamTaxonomy.Failure)
		}

		/// nếu không tồn tại method hợp lệ
	default:
		// Từ chối nếu phương thức MFA không thuộc các loại được hỗ trợ.
		loginOutcome = iamTaxonomy.Failure
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrMFAInvalid, errors.New("MFA method not supported"), iamTaxonomy.Failure)
	}

	// =======================================================================
	// BƯỚC 4.5: PHÂN GIẢI ZONE CODE SANG ZONE ID (ZONE CODE RESOLUTION)
	// =======================================================================

	zoneID := "global"
	if !strings.EqualFold(req.ZoneCode, "global") {
		// Gọi L1 cache registry để dịch zone_code thành zone_id
		val, err := s.cacheEngine.GetOrLoad(ctx, "zone_by_code", req.ZoneCode)
		if err != nil {
			loginOutcome = iamTaxonomy.GetL1CacheFail
			// Cache L1 lỗi là lỗi hệ thống 500 -> ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.GetL1CacheFail)
		}
		resolvedZoneID, ok := val.(string)
		if !ok || resolvedZoneID == "" {
			loginOutcome = iamTaxonomy.InvalidArgument
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, errors.New("zone id resolved is empty"), iamTaxonomy.InvalidArgument)
		}

		// Đảm bảo resolvedZoneID là dạng UUID hợp lệ
		if _, parseErr := uuid.Parse(resolvedZoneID); parseErr != nil {
			loginOutcome = iamTaxonomy.InvalidArgument
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parseErr, iamTaxonomy.InvalidArgument)
		}
		zoneID = resolvedZoneID
	}

	// =======================================================================
	// BƯỚC 5: TẠO CẶP ĐỊNH DANH PHIÊN CHẠY (RUNTIME SESSION CONFIGURATION)
	// =======================================================================

	// Sinh mã UUIDv7 ngẫu nhiên cho Access Key để định danh phiên làm việc hiện tại
	accessKey, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		loginOutcome = iamTaxonomy.UuidGenerateFail
		// UUID v7 sinh lỗi trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, uuidErr, iamTaxonomy.UuidGenerateFail)
	}
	// Sinh khóa bí mật Access Secret ngẫu nhiên dài 64 bytes có tính mật mã bảo mật cao
	accessSecret, genErr := security.GenerateToken(64)
	if genErr != nil {
		loginOutcome = iamTaxonomy.TokenGenerateFail
		// GenerateToken fail trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, genErr, iamTaxonomy.TokenGenerateFail)
	}

	// --- BƯỚC 6: ĐĂNG KÝ VÀ LIÊN KẾT THIẾT BỊ VẬT LÝ VỚI DATABASE ---
	// Tạo dấu vân tay (Fingerprint) của khóa công khai bằng hàm băm SHA256.
	fp := sha256.Sum256([]byte(canonicalPublicKey))
	fph := hex.EncodeToString(fp[:])

	// nếu client send uuid.Nil thì server sẽ sinh ra 1 uuid
	// biết nguồn gốc của client device id => ra quyết định
	clientDeviceProvenance := "client"
	if req.ClientDeviceID == uuid.Nil {
		req.ClientDeviceID = uuid.New()
		clientDeviceProvenance = "server-bootstrap"
	}

	// Ghi nhận thiết bị vật lý của Admin xuống Postgres Database.
	timeDeviceBinding := time.Now()
	deviceBinding, bindErr := s.repo.UpsertAdminDeviceBinding(ctx, iamEntity.AdminDeviceBindingInput{
		ID:                   req.ClientDeviceID,
		DeviceName:           req.DeviceName,
		PublicKey:            canonicalPublicKey,
		PublicKeyFingerprint: fph,
		Now:                  timeDeviceBinding,
	})
	if bindErr != nil {
		// nếu thiết bị đã từng bị thu hồi từ trước đó => báo lỗi
		if errors.Is(bindErr, iamTaxonomy.ErrDeviceRevoked) {
			loginOutcome = iamTaxonomy.Failure
			iamMetrics.Downstream("repo", workflow, "UpsertAdminDeviceBinding", iamTaxonomy.Failure, time.Since(timeDeviceBinding), bindErr)
			return iamEntity.AdminLoginResult{}, apperr.Wrap(bindErr, bindErr, iamTaxonomy.Failure)
		}
		// nếu thiết bị bị cách ly từ trước đó => báo lỗi
		if errors.Is(bindErr, iamTaxonomy.ErrDeviceQuarantined) {
			loginOutcome = iamTaxonomy.Failure
			iamMetrics.Downstream("repo", workflow, "UpsertAdminDeviceBinding", iamTaxonomy.Failure, time.Since(timeDeviceBinding), bindErr)
			return iamEntity.AdminLoginResult{}, apperr.Wrap(bindErr, bindErr, iamTaxonomy.Failure)
		}
		// các lỗi khác => báo lỗi
		loginOutcome = iamTaxonomy.FailureUnknown
		iamMetrics.Downstream("repo", workflow, "UpsertAdminDeviceBinding", iamTaxonomy.FailureUnknown, time.Since(timeDeviceBinding), bindErr)
		// Lỗi ghi DB không mong đợi trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, bindErr, iamTaxonomy.FailureUnknown)
	}

	// --- BƯỚC 7: KÝ SỐ MẬT MÃ ACCESS TOKEN JWT (PRACTICAL FAIL-FAST) ---
	// Sinh Token ID duy nhất sử dụng UUIDv7 để phục vụ JTI Claim giúp định danh chính xác JWT.
	adminJTI, jtiErr := uuid.NewV7()
	if jtiErr != nil {
		loginOutcome = iamTaxonomy.UuidGenerateFail
		// UUID v7 sinh lỗi trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, jtiErr, iamTaxonomy.UuidGenerateFail)
	}

	// Ký admin_api token sử dụng CacheRegistry và coreEntity.RuntimeSecrets
	val, err = s.cacheEngine.GetOrLoad(ctx, "admin_api_key", "")
	if err != nil {
		loginOutcome = iamTaxonomy.GetL1CacheFail
		// Cache L1 lỗi -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.GetL1CacheFail)
	}
	secrets, ok := val.(*coreEntity.RuntimeSecrets)
	if !ok || secrets == nil {
		loginOutcome = iamTaxonomy.FailureUnknown
		// Secrets cấu hình sai -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, errors.New("invalid runtime secrets type"), iamTaxonomy.FailureUnknown)
	}

	adminAPIToken, signErr := security.SignWithSecret(security.Claims{
		Subject:   "admin",
		AccessKey: accessKey.String(),
		TokenID:   adminJTI.String(),
		TokenUse:  "admin_api",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.cfg.Security.AdminSessionTTL).Unix(),
		ZoneID:    zoneID,
	}, secrets.Active.Secret)
	if signErr != nil {
		loginOutcome = iamTaxonomy.TokenGenerateFail
		// JWT Sign lỗi -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, signErr, iamTaxonomy.TokenGenerateFail)
	}

	// --- BƯỚC 8: THIẾT LẬP VÀ LIÊN KẾT PHIÊN LÀM VIỆC TRONG REDIS ---
	// Khởi tạo bản ghi Live Runtime Session hoàn chỉnh trong Redis Cache sử dụng cacheEngine L2 Set.
	now = time.Now().UTC()
	session := map[string]interface{}{
		"access_key":         accessKey.String(),
		"access_secret_hash": security.HashTokenSHA256(accessSecret),
		"tracked_device_id":  deviceBinding.ID.String(),
		"device_public_key":  canonicalPublicKey,
		"token_jti":          adminJTI.String(),
		"version":            1,
		"last_seen_at":       now.UTC().Unix(),
	}

	if err := s.cacheEngine.L2.Set(ctx, "admin_access_session:"+accessKey.String(), session, 1, s.cfg.Security.AdminSessionTTL); err != nil {
		loginOutcome = iamTaxonomy.SetAccessSessionFail
		iamMetrics.Downstream("cacheEngine", workflow, "L2.Set", iamTaxonomy.SetAccessSessionFail, time.Since(now), err)
		// Set access session lỗi -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.SetAccessSessionFail)
	}

	// --- BƯỚC 9: TRẢ VỀ KẾT QUẢ ĐĂNG NHẬP THÀNH CÔNG ---
	// Trả về AdminLoginResult chứa JWT, Access Key, Access Secret của phiên và thời gian hết hạn.
	return iamEntity.AdminLoginResult{
		AdminAPIToken:            adminAPIToken,
		AccessKey:                accessKey.String(),
		AccessSecret:             accessSecret,
		ClientDeviceID:           deviceBinding.ID,
		ClientDeviceIDProvenance: string(clientDeviceProvenance),
		ExpiresAt:                now.Add(s.cfg.Security.AdminSessionTTL),
	}, nil
}

// RefreshAdminSession cấp lại admin_api_token cho device đang active mà không
// yêu cầu login lại bằng API key + MFA.
func (s *AdminAPIKeyService) RefreshAdminSession(ctx context.Context, zoneCode string, ip *string, userAgent *string) (iamEntity.AdminLoginResult, error) {
	// --- KHỞI TẠO ĐO LƯỜNG TELEMETRY & METRICS ---

	workflow := "admin_refresh"
	refreshOutcome := iamTaxonomy.Success

	defer func() {
		iamMetrics.ServiceCall(workflow, refreshOutcome)
	}()

	// --- BƯỚC 1: TRÍCH XUẤT ACCESS KEY TỪ GO CONTEXT ---
	// Trích xuất accessKey trực tiếp từ Go standard context thay vì nhận qua tham số
	accessKeyVal := ctx.Value(constant.ContextKeyAdminAccessKey)
	accessKey, ok := accessKeyVal.(string)
	if !ok || strings.TrimSpace(accessKey) == "" {
		refreshOutcome = iamTaxonomy.InvalidArgument
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, refreshOutcome)
	}

	// --- BƯỚC 2: PHÂN GIẢI MÃ PHÂN VÙNG THÀNH UUID QUA L1 CACHE REGISTRY ---
	var resolvedZoneID string
	if !strings.EqualFold(zoneCode, "global") {
		// Gọi L1 cache để phân giải zone_code -> zone_id (UUID)
		val, err := s.cacheEngine.GetOrLoad(ctx, "zone_by_code", zoneCode)
		if err != nil {
			refreshOutcome = iamTaxonomy.GetL1CacheFail
			// Lỗi Cache L1 -> ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.GetL1CacheFail)
		}
		zoneIDStr, ok := val.(string)
		if !ok || zoneIDStr == "" {
			refreshOutcome = iamTaxonomy.ZoneUnavailable
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrZoneUnavailable, fmt.Errorf("invalid zone ID resolved from code: %s", zoneCode), iamTaxonomy.ZoneUnavailable)
		}
		resolvedZoneID = zoneIDStr
	}

	// --- BƯỚC 3: TRUY VẤN VÀ KIỂM TRA PHIÊN LÀM VIỆC HIỆN TẠI TRONG REDIS ---

	now := time.Now()
	payload, _, exists, err := s.cacheEngine.L2.Get(ctx, "admin_access_session:"+accessKey)
	if err != nil {
		iamMetrics.Downstream("sessionCache", workflow, "GetAccessSession", iamTaxonomy.GetL2CacheFail, time.Since(now), err)
		refreshOutcome = iamTaxonomy.GetL2CacheFail
		// Lỗi Cache L2 -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.GetL2CacheFail)
	}
	if !exists {
		iamMetrics.Downstream("sessionCache", workflow, "GetAccessSession", iamTaxonomy.InvalidSession, time.Since(now), err)
		refreshOutcome = iamTaxonomy.InvalidSession
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.InvalidSession)
	}

	var runtimeRecord struct {
		AccessKey         string `json:"access_key"`
		AccessSecretHash  string `json:"access_secret_hash"`
		TrackedDeviceID   string `json:"tracked_device_id"`
		DevicePublicKey   string `json:"device_public_key"`
		TokenJTI          string `json:"token_jti"`
		Version           int64  `json:"version"`
		LastSeenAt        int64  `json:"last_seen_at"`
		LastSeenIP        string `json:"last_seen_ip"`
		LastSeenUserAgent string `json:"last_seen_user_agent"`
	}
	if err := json.Unmarshal(payload, &runtimeRecord); err != nil {
		iamMetrics.Downstream("sessionCache", workflow, "GetAccessSession", iamTaxonomy.GetL2CacheFail, time.Since(now), err)
		refreshOutcome = iamTaxonomy.GetL2CacheFail
		// Lỗi Unmarshal -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.GetL2CacheFail)
	}

	// --- BƯỚC 4: THỰC THI SO SÁNH & GIA HẠN PHIÊN (COMPARE-AND-SWAP / Touch) ---
	// Atomic CAS LUA Script trên Redis để kiểm chứng version và thiết lập trực tiếp TTL của phiên cũ về 10 giây.
	// Việc thiết lập trực tiếp 10 giây ở đây giúp tối ưu hóa hiệu năng, loại bỏ hoàn toàn 1 lần ghi/RTT dư thừa xuống Redis ở cuối luồng.
	ipValue := ""
	if ip != nil {
		ipValue = strings.TrimSpace(*ip)
	}
	uaValue := ""
	if userAgent != nil {
		uaValue = strings.TrimSpace(*userAgent)
	}

	dataKey := "{admin_access_session:" + accessKey + "}:data"
	versionKey := "{admin_access_session:" + accessKey + "}:version"

	casLua := `
local current_ver = redis.call('GET', KEYS[2])
if not current_ver then
  return 0
end
if tonumber(current_ver) ~= tonumber(ARGV[1]) then
  return 0
end

local raw_data = redis.call('GET', KEYS[1])
if not raw_data then
  return 0
end

local obj = cjson.decode(raw_data)
obj.version = tonumber(current_ver) + 1
obj.last_seen_at = tonumber(ARGV[3])

local newIp = ARGV[4]
local newUA = ARGV[5]
if newIp ~= '' and tostring(obj.last_seen_ip or '') ~= newIp then
  obj.last_seen_ip = newIp
  obj.last_seen_dirty = true
end
if newUA ~= '' and tostring(obj.last_seen_user_agent or '') ~= newUA then
  obj.last_seen_user_agent = newUA
  obj.last_seen_dirty = true
end

local payload = cjson.encode(obj)
redis.call('SET', KEYS[1], payload, 'EX', tonumber(ARGV[2]))
redis.call('SET', KEYS[2], tostring(obj.version), 'EX', tonumber(ARGV[2]))
return 1
`

	resVal, casErr := s.cacheEngine.Exec.Execute(ctx, casLua, []string{dataKey, versionKey},
		runtimeRecord.Version, 10, time.Now().UTC().Unix(), ipValue, uaValue)
	if casErr != nil {
		refreshOutcome = iamTaxonomy.GetL2CacheFail
		// Exec lock/touch lỗi -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, casErr, iamTaxonomy.GetL2CacheFail)
	}
	resInt, _ := resVal.(int64)
	if resInt != 1 {
		refreshOutcome = iamTaxonomy.InvalidArgument
		// CAS thất bại tức là phiên làm việc không hợp lệ (đã bị xoay/hết hạn hoặc version không khớp)
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamTaxonomy.InvalidArgument)
	}

	now = time.Now().UTC()

	// --- BƯỚC 5: SINH MỚI BỘ BA TRINITY CREDENTIALS (ACCESS KEY, SECRET, JTI) ---
	// Sinh mới access key (UUID v7)
	accessKeyNewUUID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		refreshOutcome = iamTaxonomy.FailureUnknown
		// UUID sinh lỗi trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, uuidErr, iamTaxonomy.FailureUnknown)
	}
	accessKeyNew := accessKeyNewUUID.String()

	// Sinh mới access secret thô (48 bytes)
	accessSecretNew, genErr := security.GenerateToken(48)
	if genErr != nil {
		refreshOutcome = iamTaxonomy.FailureUnknown
		// GenerateToken fail trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, genErr, iamTaxonomy.FailureUnknown)
	}

	// Sinh mới token JTI (UUID v7)
	tokenJTINewUUID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		refreshOutcome = iamTaxonomy.FailureUnknown
		// UUID sinh lỗi trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, uuidErr, iamTaxonomy.FailureUnknown)
	}

	// --- BƯỚC 7: THIẾT LẬP PHIÊN LÀM VIỆC MỚI VÀO REDIS CACHE ---
	sessionNew := map[string]interface{}{
		"access_key":           accessKeyNew,
		"access_secret_hash":   security.HashTokenSHA256(accessSecretNew),
		"tracked_device_id":    runtimeRecord.TrackedDeviceID,
		"device_public_key":    runtimeRecord.DevicePublicKey,
		"token_jti":            tokenJTINewUUID.String(),
		"version":              1,
		"last_seen_at":         now.Unix(),
		"last_seen_ip":         runtimeRecord.LastSeenIP,
		"last_seen_user_agent": runtimeRecord.LastSeenUserAgent,
	}

	if err := s.cacheEngine.L2.Set(ctx, "admin_access_session:"+accessKeyNew, sessionNew, 1, s.cfg.Security.AdminSessionTTL); err != nil {
		refreshOutcome = iamTaxonomy.SetAccessSessionFail
		// Set access session thất bại trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.SetAccessSessionFail)
	}

	// --- BƯỚC 8: KÝ LẠI TOKEN JWT MỚI VỚI ZONEID MỤC TIÊU ---
	valNew, errNew := s.cacheEngine.GetOrLoad(ctx, "admin_api_key", "")
	if errNew != nil {
		refreshOutcome = iamTaxonomy.FailureUnknown
		// Cache L1 lỗi -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, errNew, iamTaxonomy.FailureUnknown)
	}
	secretsNew, okNew := valNew.(*coreEntity.RuntimeSecrets)
	if !okNew || secretsNew == nil {
		refreshOutcome = iamTaxonomy.FailureUnknown
		// Cấu hình secrets lỗi -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, errors.New("invalid runtime secrets type"), iamTaxonomy.FailureUnknown)
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
		refreshOutcome = iamTaxonomy.FailureUnknown
		// Ký JWT lỗi -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, signErr, iamTaxonomy.FailureUnknown)
	}

	trackedUUID, _ := uuid.Parse(runtimeRecord.TrackedDeviceID)

	return iamEntity.AdminLoginResult{
		AdminAPIToken:  adminAPITokenNew,
		AccessKey:      accessKeyNew,
		AccessSecret:   accessSecretNew,
		ClientDeviceID: trackedUUID,
		ExpiresAt:      now.Add(s.cfg.Security.AdminSessionTTL),
	}, nil
}

// AdminLogout thực hiện phế bỏ phiên làm việc của Admin ngay lập tức bằng cách xóa access key khỏi Redis Cache.
// Để bảo toàn thông tin thiết bị cuối cùng mà không làm ảnh hưởng đến thời gian phản hồi (latency),
// việc cập nhật thông tin thiết bị xuống Postgres Database được thực hiện bất đồng bộ (Asynchronous Background Flush)
// dưới cơ chế bảo vệ cắt tải chủ động (Load Shedding) sử dụng context timeout 1 giây và chỉ ghi khi thực sự thay đổi (LastSeenDirty = true).
func (s *AdminAPIKeyService) AdminLogout(ctx context.Context, accessKey string, ip *string, userAgent *string) error {
	// 1. Đọc nhanh thông tin session hiện tại trong Redis để lấy dữ liệu thiết bị (nếu có).
	payload, _, exists, err := s.cacheEngine.L2.Get(ctx, "admin_access_session:"+accessKey)
	if err != nil {
		// Cache L2 lỗi -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.FailureUnknown)
	}

	var runtimeRecord *struct {
		AccessKey         string `json:"access_key"`
		AccessSecretHash  string `json:"access_secret_hash"`
		TrackedDeviceID   string `json:"tracked_device_id"`
		DevicePublicKey   string `json:"device_public_key"`
		TokenJTI          string `json:"token_jti"`
		Version           int64  `json:"version"`
		LastSeenAt        int64  `json:"last_seen_at"`
		LastSeenIP        string `json:"last_seen_ip"`
		LastSeenUserAgent string `json:"last_seen_user_agent"`
		LastSeenDirty     bool   `json:"last_seen_dirty"`
	}

	if exists {
		var rec struct {
			AccessKey         string `json:"access_key"`
			AccessSecretHash  string `json:"access_secret_hash"`
			TrackedDeviceID   string `json:"tracked_device_id"`
			DevicePublicKey   string `json:"device_public_key"`
			TokenJTI          string `json:"token_jti"`
			Version           int64  `json:"version"`
			LastSeenAt        int64  `json:"last_seen_at"`
			LastSeenIP        string `json:"last_seen_ip"`
			LastSeenUserAgent string `json:"last_seen_user_agent"`
			LastSeenDirty     bool   `json:"last_seen_dirty"`
		}
		if err := json.Unmarshal(payload, &rec); err == nil {
			runtimeRecord = &rec
		}
	}

	// 2. PHẾ BỎ PHIÊN LÀM VIỆC LẬP TỨC (SECURITY EXTREMELY CRITICAL)
	// Thực hiện xóa session khỏi Redis trước để đảm bảo phiên chạy bị vô hiệu hóa ngay lập tức.
	if err := s.cacheEngine.L2.Delete(ctx, "admin_access_session:"+accessKey); err != nil {
		// Cache L2 xóa lỗi -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamTaxonomy.FailureUnknown)
	}

	// 3. CẬP NHẬT TRẠNG THÁI THIẾT BỊ BẤT ĐỒNG BỘ (ASYNCHRONOUS DB FLUSH)
	// Đẩy tác vụ ghi DB xuống một background goroutine để không block luồng phản hồi chính (Latency < 1ms).
	if runtimeRecord != nil && strings.TrimSpace(runtimeRecord.TrackedDeviceID) != "" {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					// Phòng chống panic trong goroutine chạy nền gây crash sập tiến trình (HA compliance)
					fmt.Printf("[CRITICAL] Panic recovered in admin logout background goroutine: %v\n", r)
				}
			}()

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
// Chỉ flush DB khi runtime đánh dấu LastSeenDirty = true.
// Lỗi flush DB hoặc xoá cache bị nuốt để vòng quét tiếp tục với device kế.
func (s *AdminAPIKeyService) FinalizeInactiveSessions(ctx context.Context, inactiveBefore time.Time, limit int) error {
	rdb := s.cacheEngine.L2.Client()

	var keys []string
	var cursor uint64
	for {
		var batch []string
		var scanErr error
		batch, cursor, scanErr = rdb.Scan(ctx, cursor, "{admin_access_session:*}:data", int64(limit)).Result()
		if scanErr != nil {
			return scanErr
		}
		keys = append(keys, batch...)
		if cursor == 0 || len(keys) >= limit {
			break
		}
	}

	if len(keys) > limit {
		keys = keys[:limit]
	}

	threshold := inactiveBefore.UTC().Unix()
	for _, key := range keys {
		keyName := key
		keyName = strings.TrimPrefix(keyName, "{admin_access_session:")
		keyName = strings.TrimSuffix(keyName, "}:data")
		accessKey := keyName

		payload, _, exists, err := s.cacheEngine.L2.Get(ctx, "admin_access_session:"+accessKey)
		if err != nil || !exists {
			continue
		}

		var record struct {
			AccessKey         string `json:"access_key"`
			AccessSecretHash  string `json:"access_secret_hash"`
			TrackedDeviceID   string `json:"tracked_device_id"`
			DevicePublicKey   string `json:"device_public_key"`
			TokenJTI          string `json:"token_jti"`
			Version           int64  `json:"version"`
			LastSeenAt        int64  `json:"last_seen_at"`
			LastSeenIP        string `json:"last_seen_ip"`
			LastSeenUserAgent string `json:"last_seen_user_agent"`
			LastSeenDirty     bool   `json:"last_seen_dirty"`
		}
		if err := json.Unmarshal(payload, &record); err != nil {
			continue
		}

		if record.LastSeenAt <= 0 || record.LastSeenAt > threshold {
			continue
		}
		// Finalize chỉ ghi last_seen nếu có delta đã track trong Redis runtime.
		if strings.TrimSpace(record.TrackedDeviceID) != "" && record.LastSeenDirty {
			_ = s.repo.TouchAdminDeviceLastSeen(ctx, record.TrackedDeviceID, optionalStringPointer(record.LastSeenIP),
				optionalStringPointer(record.LastSeenUserAgent), time.Unix(record.LastSeenAt, 0).UTC())
		}
		_ = s.cacheEngine.L2.Delete(ctx, "admin_access_session:"+accessKey)
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

// GetPublicKeyFromSession lấy public key từ session cache, nếu không có sẽ load từ DB và cập nhật lại cache.
func (s *AdminAPIKeyService) GetPublicKeyFromSession(ctx context.Context, accessKey string) (string, error) {
	payload, version, exists, err := s.cacheEngine.L2.Get(ctx, "admin_access_session:"+accessKey)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}

	var record struct {
		AccessKey         string `json:"access_key"`
		AccessSecretHash  string `json:"access_secret_hash"`
		TrackedDeviceID   string `json:"tracked_device_id"`
		DevicePublicKey   string `json:"device_public_key"`
		TokenJTI          string `json:"token_jti"`
		Version           int64  `json:"version"`
		LastSeenAt        int64  `json:"last_seen_at"`
		LastSeenIP        string `json:"last_seen_ip"`
		LastSeenUserAgent string `json:"last_seen_user_agent"`
	}
	if err := json.Unmarshal(payload, &record); err != nil {
		return "", err
	}

	if pubKey := strings.TrimSpace(record.DevicePublicKey); pubKey != "" {
		return pubKey, nil
	}
	trackedDeviceID := strings.TrimSpace(record.TrackedDeviceID)
	if trackedDeviceID == "" {
		return "", nil
	}
	pubKey, err := s.repo.GetPublicKeyByDeviceID(ctx, trackedDeviceID)
	if err != nil {
		return "", err
	}

	// Đồng bộ key ngược lại L2 Cache
	record.DevicePublicKey = pubKey
	_ = s.cacheEngine.L2.Set(ctx, "admin_access_session:"+accessKey, record, version, 30*time.Minute)

	return pubKey, nil
}
