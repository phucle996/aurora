package middleware

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"controlplane/internal/security"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// 📌 ĐỊNH NGHĨA HẰNG SỐ & MÔ HÌNH CACHE (CONSTANTS & CACHE STRUCTS)
// ============================================================================

// stepUpSecretTTL là thời gian tồn tại tối đa (TTL) của TOTP secret đã được giải mã trong cache.
// Giúp giới hạn tần suất thực hiện giải mã crypto (CPU-heavy) trên mỗi request.
const stepUpSecretTTL = 3 * time.Minute

// stepUpCacheItem lưu trữ thông tin cache của secret đã giải mã.
type stepUpCacheItem struct {
	sourceKey string    // Khóa định danh nguồn (kết hợp thời gian cập nhật & mã băm ciphertext)
	secret    string    // Plaintext TOTP secret sau khi đã giải mã thành công
	expiresAt time.Time // Thời điểm cache này hết hạn
}

// ============================================================================
// 🔌 GIAO DIỆN & CONTRACT ĐẦU VÀO (INTERFACES & FUNCTION TYPES)
// ============================================================================

// AdminStepUp2FASecretLoader định nghĩa contract để tải mã cấu hình 2FA (dưới dạng mã hóa).
type AdminStepUp2FASecretLoader interface {
	Load(ctx context.Context) (cipherText string, updatedAt time.Time, err error)
}

// AdminStepUp2FASecretLoaderFunc là một adapter cho phép sử dụng các hàm thường làm Loader.
type AdminStepUp2FASecretLoaderFunc func(ctx context.Context) (cipherText string, updatedAt time.Time, err error)

// Load thực thi gọi hàm adapter.
func (f AdminStepUp2FASecretLoaderFunc) Load(ctx context.Context) (cipherText string, updatedAt time.Time, err error) {
	return f(ctx)
}

// ============================================================================
// 🔒 BIẾN TOÀN CỤC & TRẠNG THÁI RUNTIME (GLOBAL STATE)
// ============================================================================
var (
	// stepUpCache quản lý cache RAM duy nhất cho plaintext secret đã giải mã.
	// Sử dụng RWMutex để đảm bảo an toàn ghi/đọc đồng thời từ nhiều goroutine.
	stepUpCache = struct {
		mu       sync.RWMutex
		snapshot stepUpCacheItem
	}{}

	// stepUpState lưu trữ hàm callback loadSecret được cấu hình từ app bootstrap.
	stepUpState = struct {
		mu         sync.RWMutex
		loadSecret func(ctx context.Context) (cipherText string, updatedAt time.Time, err error)
	}{}
)

// InitAdminCriticalStepUp2FA khởi tạo runtime cho middleware Step-Up 2FA.
// Hàm này phải được gọi một lần tại bootstrap của ứng dụng trước khi route hoạt động.
//
// 🎯 LÝ DO TỐI ƯU HÓA THIẾT KẾ & HIỆU NĂNG (ARCHITECTURAL & PERFORMANCE OPTIMIZATIONS):
//   1. Tách biệt hoàn toàn Dependency (Decoupling):
//      - Middleware không trực tiếp import gói IAM hay DB Repository để tránh lỗi vòng lặp import (circular import).
//      - Thay vào đó, nó nhận vào một interface generic `AdminStepUp2FASecretLoader`. Mọi chi tiết truy vấn DB
//        hoặc caching trung gian được che giấu hoàn toàn phía sau hàm loader này.
//   2. Hỗ trợ thay thế/đổi cấu hình động (Dynamic Hot-Reloading):
//      - Sử dụng `sync.RWMutex` bảo vệ con trỏ hàm `loadSecret`. Cho phép ứng dụng thay đổi cơ chế tải 
//        hoặc reload cấu hình nóng tại runtime một cách an toàn luồng, không cần khởi động lại Server.
//   3. Tách biệt tầng Cache tránh CPU-Bound (Decryption Cache):
//      - Phía triển khai loader (ở file app/module.go) có nhiệm vụ cache ciphertext (dữ liệu mã hóa) để tránh DB hit.
//      - Còn ở đây, middleware thực hiện cache plaintext (dữ liệu giải mã) trong bộ nhớ RAM (`stepUpCache`) trong 3 phút.
//      - Thiết kế 2 lớp này giúp loại bỏ hoàn toàn việc giải mã mã hóa bằng CPU liên tục trên mỗi request nghiệp vụ nhạy cảm,
//        giảm tải tính toán đáng kể cho CPU ở các hệ thống chịu tải cao.
func InitAdminCriticalStepUp2FA(loader AdminStepUp2FASecretLoader) error {
	if loader == nil {
		return errors.New("admin critical step-up: load 2fa secret is required")
	}

	stepUpState.mu.Lock()
	stepUpState.loadSecret = loader.Load
	stepUpState.mu.Unlock()
	return nil
}

// ============================================================================
// 🛡️ MIDDLEWARE: ADMIN CRITICAL STEP-UP 2FA (MFA LẦN 2 CHO HÀNH ĐỘNG NHẠY CẢM)
// ============================================================================

// AdminCriticalStepUp2FA yêu cầu xác thực yếu tố thứ 2 (MFA) tức thời đối với các hành động admin quan trọng.
// Nhằm ngăn chặn trường hợp phiên làm việc chính bị chiếm đoạt (Session Hijacking) thực hiện các lệnh nhạy cảm.
func AdminCriticalStepUp2FA() gin.HandlerFunc {
	return func(c *gin.Context) {
		// --------------------------------------------------------------------
		// B1: KIỂM TRA TRẠNG THÁI KHỞI TẠO CỦA ĐẦU TẢI BẢO MẬT (LOADER)
		// --------------------------------------------------------------------
		stepUpState.mu.RLock()
		loadSecret := stepUpState.loadSecret
		stepUpState.mu.RUnlock()
		if loadSecret == nil {
			// Hệ thống chưa được wiring loader -> Trả lỗi 503 để tự bảo vệ (Fail-Closed)
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// B2: LẤY VÀ KIỂM TRA ĐỊNH DẠNG MÃ TOTP TỪ HEADER YÊU CẦU
		// --------------------------------------------------------------------
		code := strings.TrimSpace(c.GetHeader(constant.HeaderAdminStepUpCode))
		if code == "" {
			// Không tìm thấy mã xác thực -> Từ chối truy cập (401)
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// Mã TOTP bắt buộc phải có độ dài 6 ký tự số:
		if len(code) != 6 {
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}
		for _, ch := range code {
			if ch < '0' || ch > '9' {
				apires.RespondUnauthorized(c, "unauthorized")
				c.Abort()
				return
			}
		}

		// --------------------------------------------------------------------
		// B3: TẢI VÀ GIẢI MÃ HOẶC TRUY XUẤT CACHE TOTP SECRET
		// --------------------------------------------------------------------
		secret, err := loadStepUpSecret(c.Request.Context(), loadSecret)
		if err != nil {
			// Gặp lỗi khi truy xuất hoặc giải mã mật mã -> Trả 503 bảo vệ hệ thống
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// B4: XÁC THỰC MÃ TOTP VỚI SECRET ĐÃ GIẢI MÃ (TIME-BASED OTP VALIDATION)
		// --------------------------------------------------------------------
		if !security.ValidateTOTP(code, secret) {
			// Sai mã xác thực -> Từ chối truy cập (401)
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// Xác thực thành công -> Cho phép chuyển sang handler nghiệp vụ tiếp theo
		c.Next()
	}
}

// ============================================================================
// 🛠️ HÀM TRỢ GIÚP NỘI BỘ (HELPER FUNCTION)
// ============================================================================

// loadStepUpSecret thực hiện tải ciphertext từ DB/Loader, đối chiếu cache, giải mã nếu cần và cập nhật cache.
func loadStepUpSecret(
	ctx context.Context,
	loadSecret func(ctx context.Context) (cipherText string, updatedAt time.Time, err error),
) (string, error) {
	// Tải dữ liệu cấu hình đã mã hóa từ DB/cơ sở lưu trữ:
	cipherText, updatedAt, err := loadSecret(ctx)
	if err != nil {
		return "", err
	}
	cipherText = strings.TrimSpace(cipherText)
	if cipherText == "" {
		return "", errors.New("admin critical step-up: totp secret is unavailable")
	}

	now := time.Now().UTC()

	// Tạo khóa định danh nguồn duy nhất (sourceKey) dựa trên thời gian cập nhật cuối và mã băm SHA256 của cipherText:
	cipherHash := sha256.Sum256([]byte(cipherText))
	sourceKey := fmt.Sprintf("%s:%x", updatedAt.UTC().Format(time.RFC3339Nano), cipherHash)

	// --------------------------------------------------------------------
	// 🚀 NHÁNH TRUY XUẤT NHANH (FAST-PATH CACHE):
	// --------------------------------------------------------------------
	stepUpCache.mu.RLock()
	cached := stepUpCache.snapshot
	stepUpCache.mu.RUnlock()

	// Nếu khóa trùng khớp và cache chưa hết hạn -> Sử dụng ngay plaintext đã cache:
	if cached.sourceKey == sourceKey && cached.secret != "" && now.Before(cached.expiresAt) {
		return cached.secret, nil
	}

	// --------------------------------------------------------------------
	// 🚀 NHÁNH GIẢI MÃ CHẬM (SLOW-PATH DECRYPT & WRITE CACHE):
	// --------------------------------------------------------------------

	// Thực hiện giải mã mật khẩu bằng khóa riêng của hệ thống (crypto decryption):
	secret, err := security.DecryptSecret(cipherText)
	if err != nil {
		return "", err
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", errors.New("admin critical step-up: decrypted totp secret is empty")
	}

	// Cập nhật kết quả đã giải mã vào cache RAM để phục vụ các request tiếp theo:
	stepUpCache.mu.Lock()
	stepUpCache.snapshot = stepUpCacheItem{
		sourceKey: sourceKey,
		secret:    secret,
		expiresAt: now.Add(stepUpSecretTTL), // Thiết lập thời gian sống TTL cho cache
	}
	stepUpCache.mu.Unlock()

	return secret, nil
}
