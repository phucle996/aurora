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
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"
	"errors"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"google.golang.org/protobuf/proto"
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

	var outcome = iamMetrics.OutcomeSuccess
	defer func() { iamMetrics.ServiceCall(ctx, outcome) }()

	// ==========================================================================
	// BƯỚC 1: ACQUIRE ROTATION LOCK TOÀN CỤC ĐỂ CHỐNG THỰC THI ĐỒNG THỜI (RACE CONDITION)
	// ==========================================================================
	lockStart := time.Now()

	lock, err := s.repo.AcquireRotationLock(ctx)
	if err != nil {
		// đo metrics số lần call bị lock chặn
		if errors.Is(err, iamTaxonomy.ErrLockAlreadyHeld) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "AcquireRotationLock", iamMetrics.OutcomeLockBusy, time.Since(lockStart), err)
			// Lock bận tức là tiền đề không thỏa mãn cho lượt xoay tiếp theo
			outcome = iamMetrics.OutcomeLockBusy
			return apperr.Wrap(iamTaxonomy.ErrPreconditionFailed, err, outcome)
		}

		// Lỗi lock không mong đợi -> trả lỗi 500 ErrInternalError
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "AcquireRotationLock", iamMetrics.OutcomeFailureUnknown, time.Since(lockStart), err)
		outcome = iamMetrics.OutcomeFailureUnknown
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, outcome)
	}
	// luôn release lock sau khi hoàn thành hoặc xảy ra lỗi để tránh deadlock
	defer lock.Release(ctx)
	// lock thành công => đo metrics
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "AcquireRotationLock", iamMetrics.OutcomeSuccess, time.Since(lockStart), nil)

	// ==========================================================================
	// BƯỚC 2: SINH NGẪU NHIÊN API KEY MỚI (PLAINTEXT)
	// ==========================================================================

	plainKey, err := security.GenerateToken(48)
	if err != nil {
		// đo metrics số lần sinh token thất bại
		outcome = iamMetrics.OutcomeFailureUnknown
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
		iamMetrics.Downstream(ctx, iamMetrics.KindTelegram, "SendMessage", iamMetrics.OutcomeFailureUnknown, time.Since(telegramStart), sendErr)
		// Telegram thông báo lỗi là sự cố hạ tầng kết nối -> ErrInternalError
		outcome = iamMetrics.OutcomeFailureUnknown
		return apperr.Wrap(iamTaxonomy.ErrInternalError, sendErr, outcome)
	}

	// ==========================================================================
	// BƯỚC 4: TẠO THỰC THỂ ADMINAPIKEY VÀ LƯU VÀO CƠ SỞ DỮ LIỆU Ở TRẠNG THÁI ACTIVE KẾ TIẾP
	// ==========================================================================
	newID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		// đo metrics số lần sinh uuid thất bại
		outcome = iamMetrics.OutcomeFailureUnknown
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
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "PrepareNextAdminAPIKey", iamMetrics.OutcomeFailureUnknown, time.Since(start), err)
		// Database Prepare API Key lỗi là lỗi kết nối hệ thống -> ErrInternalError
		outcome = iamMetrics.OutcomeFailureUnknown
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, outcome)
	}

	// Bước 5: Xóa cờ chỉ định xoay vòng khóa khẩn cấp (nếu có).
	// use defer để đảm bảo luôn xóa cờ dù có lỗi xảy ra sau này
	err = s.cacheEngine.L2.Delete(ctx, "iam:admin_key_rotation:required")
	if err != nil {
		// đo metrics số lần xóa cờ thất bại
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "DeleteRotationRequired", iamMetrics.OutcomeFailureUnknown, time.Since(start), err)
		// Lỗi Redis Delete trả ErrInternalError
		outcome = iamMetrics.OutcomeFailureUnknown
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
	// Tiêm operation name vào context để service/repo calls bên dưới nhận diện đúng workflow
	ctx = constant.WithOperation(ctx, "admin_key_rotation_trigger")

	var outcome = iamMetrics.OutcomeSuccess
	var runMetric bool
	defer func() {
		if runMetric {
			iamMetrics.ServiceCall(ctx, outcome)
		}
	}()

	// ==========================================================
	// BƯỚC 1: CHECK TRIGGER XOAY VÒNG KEY ADMIN ĐỂ ĐẢM BẢO HA VÀ CHỈ ROTATE KHI CÓ YÊU CẦU
	// ==========================================================
	l2Start := time.Now()
	_, _, required, err := s.cacheEngine.L2.Get(ctx, "iam:admin_key_rotation:required")
	if err != nil {
		runMetric = true
		outcome = iamMetrics.OutcomeFailureUnknown
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "GetRotationRequired", iamMetrics.OutcomeFailureUnknown, time.Since(l2Start), err)
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
		outcome = iamMetrics.OutcomeFailureUnknown
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
	// Tiêm operation name vào context để service/repo calls bên dưới nhận diện đúng workflow
	ctx = constant.WithOperation(ctx, "admin_key_bootstrap")
	var outcome = iamMetrics.OutcomeSuccess
	defer func() {
		iamMetrics.ServiceCall(ctx, outcome)
	}()

	// ==========================================================
	// BƯỚC 1: KHỞI TẠO CỜ LOCK ĐỂ ĐẢM BẢO CHẠY ĐƠN CHIẾC DUY NHẤT
	// ==========================================================
	lockStart := time.Now()
	lock, err := s.repo.AcquireBootstrapLock(ctx)
	if err != nil {
		outcome = iamMetrics.OutcomeFailureUnknown
		// lock thất bại => đo metrics lỗi
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "AcquireBootstrapLock", iamMetrics.OutcomeFailureUnknown, time.Since(lockStart), err)
		if errors.Is(err, iamTaxonomy.ErrLockAlreadyHeld) {
			// Lock bận tức là có tiến trình bootstrap song song khác đang chạy
			return apperr.Wrap(iamTaxonomy.ErrLockAlreadyHeld, err, iamMetrics.OutcomeFailureUnknown)
		}
		// Lỗi kết nối DB khi lấy lock -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}
	defer lock.Release(ctx)
	// lock thành công => đo metrics
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "AcquireBootstrapLock", iamMetrics.OutcomeSuccess, time.Since(lockStart), nil)

	// ==========================================================
	// BƯỚC 2: KIỂM TRA TIỀN ĐỀ ĐIỀU KIỆN (PRECONDITION)
	// - Đảm bảo không có active key nào tồn tại trong DB trước khi bootstrap
	// - Nếu có => lỗi không cho phép bootstrap
	// ==========================================================
	precondStart := time.Now()
	active, err := s.repo.GetActiveAdminAPIKey(ctx)
	if err != nil {
		outcome = iamMetrics.OutcomeFailureUnknown
		// nếu repo lỗi => trả về lỗi và ghi metrics downstream thất bại
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetActiveAdminAPIKey", iamMetrics.OutcomeFailureUnknown, time.Since(precondStart), err)
		// Lỗi truy vấn database là lỗi kết nối hệ thống -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}
	// nếu active không nil => đã có key => trả về lỗi và ghi metrics downstream thất bại
	if active != nil {
		outcome = iamMetrics.OutcomePreConditionFailed
		// đo metrics tiền đề đã có active key
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetActiveAdminAPIKey", iamMetrics.OutcomePreConditionFailed, time.Since(precondStart), iamTaxonomy.ErrActionNotAllowed)
		// Hệ thống đã được khởi tạo từ trước, không được phép bootstrap lại
		return apperr.Wrap(iamTaxonomy.ErrPreconditionFailed, errors.New("admin API key already exists in system"), iamMetrics.OutcomePreConditionFailed)
	}
	// hoàn tất pre-condition thành công => ghi metrics
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetActiveAdminAPIKey", iamMetrics.OutcomeSuccess, time.Since(precondStart), nil)

	// ==========================================================
	// BƯỚC 3: TẠO ADMIN API KEY VÀ DỮ LIỆU CẦN THIẾT
	// ==========================================================

	// Tạo ngẫu nhiên Admin API key nguyên bản (plaintext) và mã hóa SHA256 để lưu DB
	plainKey, err := security.GenerateToken(48)
	if err != nil {
		outcome = iamMetrics.OutcomeFailureUnknown
		// Sinh token ngẫu nhiên thất bại là lỗi phát hành token nội bộ -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
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
		outcome = iamMetrics.OutcomeFailureUnknown
		// Sinh TOTP lỗi trả ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}
	secretCipher, err := security.EncryptSecret(totpResult.Secret)
	if err != nil {
		outcome = iamMetrics.OutcomeFailureUnknown
		// Mã hóa secret lỗi trả ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
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
			outcome = iamMetrics.OutcomeFailureUnknown
			// Sinh recovery code lỗi trả ErrInternalError
			return apperr.Wrap(iamTaxonomy.ErrInternalError, genErr, iamMetrics.OutcomeFailureUnknown)
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
		outcome = iamMetrics.OutcomeFailureUnknown
		// nếu repo lỗi => trả về lỗi và ghi metrics downstream thất bại
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "Bootstrap", iamMetrics.OutcomeFailureUnknown, time.Since(start), err)
		// Lỗi ghi DB khi bootstrap là lỗi kết nối hệ thống -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}
	// nếu repo thành công => ghi metrics
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "Bootstrap", iamMetrics.OutcomeSuccess, time.Since(start), nil)

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
			iamMetrics.Downstream(ctx, iamMetrics.KindTelegram, "SendMessage", iamMetrics.OutcomeSuccess, time.Since(start), nil)
			return nil
		} else {
			// nếu send fail => lưu lỗi
			notifyErr = sendErr
			// ghi metrics
			iamMetrics.Downstream(ctx, iamMetrics.KindTelegram, "SendMessage", iamMetrics.OutcomeFailureUnknown, time.Since(start), notifyErr)
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
		outcome = iamMetrics.OutcomeFailureUnknown
		// ghi metrics
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RollbackBootstrap", iamMetrics.OutcomeFailureUnknown, time.Since(start), rbErr)
		// Lỗi rollback là lỗi kết nối hệ thống -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, rbErr, iamMetrics.OutcomeFailureUnknown)
	}
	// ghi metrics
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RollbackBootstrap", iamMetrics.OutcomeSuccess, time.Since(start), nil)
	outcome = iamMetrics.OutcomeFailureUnknown
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

	loginOutcome := iamMetrics.OutcomeSuccess
	defer func() { iamMetrics.ServiceCall(ctx, loginOutcome) }()

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
		loginOutcome = iamMetrics.OutcomeLockBusy
		// Ghi nhận chỉ số downstream với nhãn chuẩn cache-engine-l2 (Redis Distributed Lock)
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineExcute, "ExecuteLock", iamMetrics.OutcomeLockBusy, time.Since(startLock), err)
		// Trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeLockBusy)
	}

	// Ghi nhận thành công downstream cho cache-engine-l2
	iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineExcute, "ExecuteLock", iamMetrics.OutcomeSuccess, time.Since(startLock), nil)
	if valInt, ok := res.(int64); !ok || valInt != 1 {
		loginOutcome = iamMetrics.OutcomeLockBusy
		// Lock bận trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, errors.New("login lock already held for this device/key"), iamMetrics.OutcomeLockBusy)
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
		loginOutcome = iamMetrics.OutcomeFailure
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, keyErr, iamMetrics.OutcomeFailure)
	}

	/// ========================================================
	// BƯỚC 2: XÁC THỰC API KEY ADMIN (API KEY VERIFICATION)
	// ========================================================

	now := time.Now()
	val, err := s.cacheEngine.GetOrLoad(ctx, "admin_api_key_active", "")
	if err != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		// Ghi nhận lỗi downstream với nhãn chuẩn cache-engine (L1 Cache)
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL1, "GetActiveAdminAPIKey", iamMetrics.OutcomeFailureUnknown, time.Since(now), err)
		// Trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}
	// Ghi nhận thành công downstream cho cache-engine (L1 Cache)
	iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL1, "GetActiveAdminAPIKey", iamMetrics.OutcomeSuccess, time.Since(now), nil)

	active, ok := val.(*iamEntity.AdminAPIKey)
	if !ok || active == nil {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidCredential, errors.New("api key not found"), iamMetrics.OutcomeInvalidCredential)
	}
	// Thực hiện băm SHA256 API Key của Client gửi lên và đối khớp trực tiếp với KeyHash được cấu hình bảo mật.
	if active.KeyHash != security.HashTokenSHA256(req.RawAPIKey) {
		loginOutcome = iamMetrics.OutcomeInvalidCredential
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "HashTokenSHA256", iamMetrics.OutcomeInvalidCredential, time.Since(now), nil)
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidCredential, errors.New("hash api key not match"), iamMetrics.OutcomeInvalidCredential)
	}

	if req.ClientDeviceID != uuid.Nil {
		existingPubKey, err := s.repo.GetPublicKeyByDeviceID(ctx, req.ClientDeviceID.String())
		if err == nil {
			// 🛡️ SRE HA & LOGIC FIX:
			//   - Tránh deadlock/lockout vô hạn: Nếu IndexedDB lưu key ở client bị xóa (do browser auto-clean, private mode)
			//     nhưng HttpOnly cookie ClientDeviceID vẫn còn, việc so khớp cứng sẽ block 100% các lần thử đăng nhập tiếp theo.
			//   - An toàn bảo mật: Do đăng nhập bắt buộc phải đi qua xác thực API Key và mã MFA (TOTP/Recovery Code) có độ
			//     bảo mật cực cao, nếu hai yếu tố trên đúng thì danh tính SRE Admin đã được đảm bảo.
			//   - Tính sẵn sàng & Tự phục hồi (Self-Healing): Khi khóa công khai gửi lên không khớp, ta ghi nhận log cảnh báo
			//     nhưng cho phép đi tiếp. Sau khi verify MFA thành công, hàm repo.UpsertAdminDeviceBinding sẽ thực hiện GHI ĐÈ
			//     (overwrite/rotate) khóa công khai mới trực tiếp vào dòng của ClientDeviceID hiện tại thông qua mệnh đề ON CONFLICT DO UPDATE.
			//     Quy trình này đảm bảo KHÔNG TẠO thiết bị mới trong database mà chỉ cập nhật khóa của thiết bị cũ.
			if existingPubKey != canonicalPublicKey {
				logger.SysWarn("iam.admin_auth.login", fmt.Sprintf("Admin device public key mismatch for device %s. Public key will be rotated upon successful MFA verification.", req.ClientDeviceID.String()))
			}
		} else if !errors.Is(err, iamTaxonomy.ErrNotFound) {
			// Lỗi truy vấn database thực sự sẽ trả ErrInternalError
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
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
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			// Trả ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
		}

		// secret đã decrypted từ cache trước đó, do đó ta có thể dùng trực tiếp mà không cần trim space
		secret, ok := val.(string)
		if !ok {
			loginOutcome = iamMetrics.OutcomeInvalidCredential
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidCredential, errors.New("2fa secret not found or empty"), iamMetrics.OutcomeInvalidCredential)
		}

		// Thực hiện giải mã cryptographic secret và đối khớp mã TOTP với chu kỳ 30 giây và sai số lệch giờ cho phép (Skew) là 1 chu kỳ.
		okTOTP, totpErr := totp.ValidateCustom(req.MFACode, secret, now, totp.ValidateOpts{
			Period:    30,
			Skew:      1,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if totpErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrMFAInvalid, totpErr, iamMetrics.OutcomeFailureUnknown)
		}
		if !okTOTP {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrMFAInvalid, errors.New("totp passcode mismatch"), iamMetrics.OutcomeFailureUnknown)
		}

		// Chống Replay TOTP bằng cơ chế Blacklist/Consume ngắn hạn trong Redis
		totpKey := "iam:admin_totp_consumed:" + strings.TrimSpace(req.MFACode)
		rdb := s.cacheEngine.L2.Client()
		setOk, setErr := rdb.SetNX(ctx, totpKey, "1", 90*time.Second).Result()
		if setErr != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			// Redis SetNX lỗi -> ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, setErr, iamMetrics.OutcomeFailureUnknown)
		}
		if !setOk {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrMFAInvalid, errors.New("totp passcode already consumed"), iamMetrics.OutcomeFailureUnknown)
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
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			// Ghi nhận lỗi downstream với nhãn chuẩn cache-engine-l2 cho phân tán lock
			iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineExcute, "Exec.Execute", iamMetrics.OutcomeFailureUnknown, time.Since(now), err)
			// Redis Exec lock lỗi -> ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
		}
		if valInt, ok := res.(int64); !ok || valInt != 1 {
			loginOutcome = iamMetrics.OutcomeLockBusy
			// Ghi nhận lock bận với nhãn chuẩn cache-engine-l2
			iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineExcute, "Exec.Execute", iamMetrics.OutcomeLockBusy, time.Since(now), nil)
			// Lock bận là lỗi xung đột concurrency hệ thống -> ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, errors.New("recovery consume lock already held"), iamMetrics.OutcomeLockBusy)
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
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "ConsumeRecoveryCode", iamMetrics.OutcomeFailureUnknown, time.Since(now), consumeErr)
			if errors.Is(consumeErr, iamTaxonomy.ErrNotFound) || errors.Is(consumeErr, iamTaxonomy.ErrRecoveryCodeInvalid) {
				// Mã không tồn tại hoặc đã sử dụng trước đó
				return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrMFAInvalid, consumeErr, iamMetrics.OutcomeFailureUnknown)
			}
			// Các lỗi DB khác trả ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, consumeErr, iamMetrics.OutcomeFailureUnknown)
		}

		/// nếu không tồn tại method hợp lệ
	default:
		// Từ chối nếu phương thức MFA không thuộc các loại được hỗ trợ.
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrMFAInvalid, errors.New("MFA method not supported"), iamMetrics.OutcomeFailureUnknown)
	}

	// =======================================================================
	// BƯỚC 4.5: PHÂN GIẢI ZONE CODE SANG ZONE ID (ZONE CODE RESOLUTION)
	// =======================================================================

	zoneID := "global"
	if !strings.EqualFold(req.ZoneCode, "global") {
		// Gọi L1 cache registry để dịch zone_code thành zone_id
		val, err := s.cacheEngine.GetOrLoad(ctx, "zone_by_code", req.ZoneCode)
		if err != nil {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			// Cache L1 lỗi là lỗi hệ thống 500 -> ErrInternalError
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
		}
		resolvedZoneID, ok := val.(string)
		if !ok || resolvedZoneID == "" {
			loginOutcome = iamMetrics.OutcomeFailure
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, errors.New("zone id resolved is empty"), iamMetrics.OutcomeFailure)
		}

		// Đảm bảo resolvedZoneID là dạng UUID hợp lệ
		if _, parseErr := uuid.Parse(resolvedZoneID); parseErr != nil {
			loginOutcome = iamMetrics.OutcomeFailure
			return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parseErr, iamMetrics.OutcomeFailure)
		}
		zoneID = resolvedZoneID
	}

	// =======================================================================
	// BƯỚC 5: TẠO CẶP ĐỊNH DANH PHIÊN CHẠY (RUNTIME SESSION CONFIGURATION)
	// =======================================================================

	// Sinh mã UUIDv7 ngẫu nhiên cho Access Key để định danh phiên làm việc hiện tại
	accessKey, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		// UUID v7 sinh lỗi trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, uuidErr, iamMetrics.OutcomeFailureUnknown)
	}
	// Sinh khóa bí mật Access Secret ngẫu nhiên dài 64 bytes có tính mật mã bảo mật cao
	accessSecret, genErr := security.GenerateToken(64)
	if genErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		// GenerateToken fail trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, genErr, iamMetrics.OutcomeFailureUnknown)
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
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "UpsertAdminDeviceBinding", iamMetrics.OutcomeFailureUnknown, time.Since(timeDeviceBinding), bindErr)
			return iamEntity.AdminLoginResult{}, apperr.Wrap(bindErr, bindErr, iamMetrics.OutcomeFailureUnknown)
		}
		// nếu thiết bị bị cách ly từ trước đó => báo lỗi
		if errors.Is(bindErr, iamTaxonomy.ErrDeviceQuarantined) {
			loginOutcome = iamMetrics.OutcomeFailureUnknown
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "UpsertAdminDeviceBinding", iamMetrics.OutcomeFailureUnknown, time.Since(timeDeviceBinding), bindErr)
			return iamEntity.AdminLoginResult{}, apperr.Wrap(bindErr, bindErr, iamMetrics.OutcomeFailureUnknown)
		}
		// các lỗi khác => báo lỗi
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "UpsertAdminDeviceBinding", iamMetrics.OutcomeFailureUnknown, time.Since(timeDeviceBinding), bindErr)
		// Lỗi ghi DB không mong đợi trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, bindErr, iamMetrics.OutcomeFailureUnknown)
	}

	// --- BƯỚC 7: KÝ SỐ MẬT MÃ ACCESS TOKEN JWT (PRACTICAL FAIL-FAST) ---
	// Sinh Token ID duy nhất sử dụng UUIDv7 để phục vụ JTI Claim giúp định danh chính xác JWT.
	adminJTI, jtiErr := uuid.NewV7()
	if jtiErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		// UUID v7 sinh lỗi trả ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, jtiErr, iamMetrics.OutcomeFailureUnknown)
	}

	// Ký admin_api token sử dụng CacheRegistry và coreEntity.RuntimeSecrets
	val, err = s.cacheEngine.GetOrLoad(ctx, "admin_api_key", "")
	if err != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		// Cache L1 lỗi -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}
	secrets, ok := val.(*coreEntity.RuntimeSecrets)
	if !ok || secrets == nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		// Secrets cấu hình sai -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, errors.New("invalid runtime secrets type"), iamMetrics.OutcomeFailureUnknown)
	}

	adminAPIToken, signErr := security.SignWithSecret(security.Claims{
		Subject:   "sre",
		AccessKey: accessKey.String(),
		TokenID:   adminJTI.String(),
		TokenUse:  "admin_api",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.cfg.Security.AdminSessionTTL).Unix(),
		ZoneID:    zoneID,
	}, secrets.Active.Secret)
	if signErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		// JWT Sign lỗi -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, signErr, iamMetrics.OutcomeFailureUnknown)
	}

	// --- BƯỚC 8: THIẾT LẬP VÀ LIÊN KẾT PHIÊN LÀM VIỆC TRONG REDIS ---
	// Khởi tạo bản ghi Live Runtime Session hoàn chỉnh trong Redis Cache sử dụng cacheEngine L2 Set.
	now = time.Now().UTC()
	pbAdmin := &iamproto.AdminAccessSession{
		AccessKey:         accessKey.String(),
		AccessSecretHash:  security.HashTokenSHA256(accessSecret),
		TrackedDeviceId:   deviceBinding.ID.String(),
		DevicePublicKey:   canonicalPublicKey,
		TokenJti:          adminJTI.String(),
		Version:           1,
		LastSeenAt:        now.UTC().Unix(),
		LastSeenIp:        "",
		LastSeenUserAgent: "",
		LastSeenDirty:     false,
	}
	session, marshalErr := proto.Marshal(pbAdmin)
	if marshalErr != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, marshalErr, iamMetrics.OutcomeFailureUnknown)
	}

	// Lưu trữ session được phân vùng theo cả Access Key và Zone ID (Zone-scoped)
	if err := s.cacheEngine.L2.Set(ctx, "admin_access_session:"+accessKey.String()+":"+zoneID, session, 1, s.cfg.Security.AdminSessionTTL); err != nil {
		loginOutcome = iamMetrics.OutcomeFailureUnknown
		// Ghi nhận lỗi set session với nhãn chuẩn cache-engine-l2
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "L2.Set", iamMetrics.OutcomeFailureUnknown, time.Since(now), err)
		// Set access session lỗi -> ErrInternalError
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
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

// AdminLogout thực hiện phế bỏ phiên làm việc của Admin ngay lập tức bằng cách xóa access key khỏi Redis Cache.
// Để bảo toàn thông tin thiết bị cuối cùng mà không làm ảnh hưởng đến thời gian phản hồi (latency),
// việc cập nhật thông tin thiết bị xuống Postgres Database được thực hiện bất đồng bộ (Asynchronous Background Flush)
// dưới cơ chế bảo vệ cắt tải chủ động (Load Shedding) sử dụng context timeout 1 giây và chỉ ghi khi thực sự thay đổi (LastSeenDirty = true).
func (s *AdminAPIKeyService) AdminLogout(ctx context.Context, ip *string, userAgent *string) error {
	// Trích xuất thông tin định danh Admin từ context
	var accessKey string
	var zoneID string
	if ident, ok := ctx.Value(constant.IdentityKey).(*constant.Identity); ok && ident != nil {
		accessKey = ident.AccessKey
		zoneID = ident.ZoneID
	}
	if strings.TrimSpace(accessKey) == "" {
		return iamTaxonomy.ErrInvalidSession
	}
	if zoneID == "" {
		zoneID = "global"
	}

	// 1. Đọc nhanh thông tin session hiện tại trong Redis để lấy dữ liệu thiết bị (nếu có).
	payload, _, exists, err := s.cacheEngine.L2.Get(ctx, "admin_access_session:"+accessKey+":"+zoneID)
	if err != nil {
		// Cache L2 lỗi -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}

	var runtimeRecord *iamproto.AdminAccessSession
	if exists {
		var rec iamproto.AdminAccessSession
		if err := proto.Unmarshal(payload, &rec); err == nil {
			runtimeRecord = &rec
		}
	}

	// 2. PHẾ BỎ PHIÊN LÀM VIỆC LẬP TỨC (SECURITY EXTREMELY CRITICAL)
	// Thực hiện xóa session khỏi Redis trước để đảm bảo phiên chạy bị vô hiệu hóa ngay lập tức.
	if err := s.cacheEngine.L2.Delete(ctx, "admin_access_session:"+accessKey+":"+zoneID); err != nil {
		// Cache L2 xóa lỗi -> ErrInternalError
		return apperr.Wrap(iamTaxonomy.ErrInternalError, err, iamMetrics.OutcomeFailureUnknown)
	}

	// 3. CẬP NHẬT TRẠNG THÁI THIẾT BỊ BẤT ĐỒNG BỘ (ASYNCHRONOUS DB FLUSH)
	// Đẩy tác vụ ghi DB xuống một background goroutine để không block luồng phản hồi chính (Latency < 1ms).
	if runtimeRecord != nil && strings.TrimSpace(runtimeRecord.TrackedDeviceId) != "" {
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

			runtimeIP := strings.TrimSpace(runtimeRecord.LastSeenIp)
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
				_ = s.repo.TouchAdminDeviceLastSeen(bgCtx, runtimeRecord.TrackedDeviceId, optionalStringPointer(runtimeIP), optionalStringPointer(runtimeUA), time.Unix(seenAtUnix, 0).UTC())
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

		// Bóc tách accessKey và zoneID từ cấu hình key phân vùng dạng <accessKey>:<zoneID>
		parts := strings.Split(keyName, ":")
		if len(parts) != 2 {
			continue
		}
		accessKey := parts[0]
		zoneID := parts[1]

		payload, _, exists, err := s.cacheEngine.L2.Get(ctx, "admin_access_session:"+accessKey+":"+zoneID)
		if err != nil || !exists {
			continue
		}

		var record iamproto.AdminAccessSession
		err = proto.Unmarshal(payload, &record)
		if err != nil {
			continue
		}

		if record.LastSeenAt <= 0 || record.LastSeenAt > threshold {
			continue
		}
		// Finalize chỉ ghi last_seen nếu có delta đã track trong Redis runtime.
		if strings.TrimSpace(record.TrackedDeviceId) != "" && record.LastSeenDirty {
			_ = s.repo.TouchAdminDeviceLastSeen(ctx, record.TrackedDeviceId, optionalStringPointer(record.LastSeenIp),
				optionalStringPointer(record.LastSeenUserAgent), time.Unix(record.LastSeenAt, 0).UTC())
		}
		_ = s.cacheEngine.L2.Delete(ctx, "admin_access_session:"+accessKey+":"+zoneID)
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
	// Trích xuất zoneID của Admin từ context
	var zoneID string
	if ident, ok := ctx.Value(constant.IdentityKey).(*constant.Identity); ok && ident != nil {
		zoneID = ident.ZoneID
	}
	if zoneID == "" {
		zoneID = "global"
	}

	payload, version, exists, err := s.cacheEngine.L2.Get(ctx, "admin_access_session:"+accessKey+":"+zoneID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}

	var record iamproto.AdminAccessSession
	err = proto.Unmarshal(payload, &record)
	if err != nil {
		return "", err
	}

	if pubKey := strings.TrimSpace(record.DevicePublicKey); pubKey != "" {
		return pubKey, nil
	}
	trackedDeviceID := strings.TrimSpace(record.TrackedDeviceId)
	if trackedDeviceID == "" {
		return "", nil
	}
	pubKey, err := s.repo.GetPublicKeyByDeviceID(ctx, trackedDeviceID)
	if err != nil {
		return "", err
	}

	// Đồng bộ key ngược lại L2 Cache có chứa zoneID
	record.DevicePublicKey = pubKey
	newPayload, marshalErr := proto.Marshal(&record)
	if marshalErr == nil {
		_ = s.cacheEngine.L2.Set(ctx, "admin_access_session:"+accessKey+":"+zoneID, newPayload, version, 30*time.Minute)
	}

	return pubKey, nil
}

// VerifyAdminTrinitySession xác thực thông tin đăng nhập của Admin/SRE qua gRPC.
// Phương thức này kiểm tra JWT token dựa trên keys của Admin từ cache registry,
// đối chiếu access_key và verify tính hoạt động của session trong Redis L2.
func (s *AdminAPIKeyService) VerifyAdminTrinitySession(ctx context.Context, token string, accessKey string, accessSecret string) (*iamEntity.VerifySessionResult, error) {
	// Bước 1: Truy xuất danh sách key ký mã hóa cho Admin từ cache registry
	val, err := s.cacheEngine.GetOrLoad(ctx, "admin_api_key", "")
	if err != nil {
		return &iamEntity.VerifySessionResult{Valid: false}, err
	}
	secrets, ok := val.(*coreEntity.RuntimeSecrets)
	if !ok || secrets == nil {
		return &iamEntity.VerifySessionResult{Valid: false}, fmt.Errorf("invalid runtime secrets type for admin")
	}

	// Bước 2: Giải mã JWT lần lượt bằng active rồi standby key
	var claims security.Claims
	parsed := false
	for _, candidate := range []coreEntity.RuntimeSecret{secrets.Active, secrets.Standby} {
		parsedClaims, parseErr := security.Parse(token, candidate.Secret)
		if parseErr == nil {
			claims = parsedClaims
			parsed = true
			break
		}
	}
	if !parsed {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	// Bước 3: Đối chiếu access_key trong token với access_key client cung cấp
	if strings.TrimSpace(claims.AccessKey) == "" || claims.AccessKey != accessKey {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	// Bước 4: Kiểm tra tính hoạt động của session từ Redis L2 cache
	// Sử dụng Zone ID từ claims để định tuyến phân vùng chính xác
	zoneID := strings.TrimSpace(claims.ZoneID)
	if zoneID == "" {
		// Fallback về global nếu không tìm thấy phân vùng trong claims
		zoneID = "global"
	}
	payload, _, exists, err := s.cacheEngine.L2.Get(ctx, "admin_access_session:"+accessKey+":"+zoneID)
	if err != nil || !exists {
		return &iamEntity.VerifySessionResult{Valid: false}, err
	}

	var session iamproto.AdminAccessSession
	err = proto.Unmarshal(payload, &session)
	if err != nil {
		return &iamEntity.VerifySessionResult{Valid: false}, err
	}

	// So sánh SHA256 hash của access_secret nhận được
	incomingHash := security.HashTokenSHA256(accessSecret)
	if session.AccessSecretHash != incomingHash {
		return &iamEntity.VerifySessionResult{Valid: false}, nil
	}

	return &iamEntity.VerifySessionResult{
		Valid:  true,
		UserID: claims.Subject,
		Role:   "SRE",
	}, nil
}
