package middleware

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"controlplane/internal/cacheengine"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
)

// ============================================================================
// 📌 HẰNG SỐ & ĐỊNH NGHĨA STRUCT HỖ TRỢ (CONSTANTS & SUPPORTING TYPES)
// ============================================================================

// sigNoncePrefix là tiền tố Redis key dùng để đánh dấu và khóa các Nonce đã sử dụng.
// Giúp chống lại các cuộc tấn công phát lại (Replay Attacks) đối với admin request.
const sigNoncePrefix = "iam:admin:critical:nonce:"

// sigRuntime chứa các tài nguyên và hàm callback được cấu hình động cho việc xác thực chữ ký.
type sigRuntime struct {
	cacheEngine *cacheengine.CacheRegistry
	rds         *goredis.Client // Redis client phục vụ ghi nhận Nonce chống replay
	nonceTTL    time.Duration   // Thời gian tồn tại của Nonce trong Redis (ví dụ: 5 phút)
	skew        time.Duration   // Độ lệch thời gian cho phép của timestamp so với clock của server
}

// sigProof đóng gói các thông tin bằng chứng chữ ký được gửi lên từ các Header HTTP.
type sigProof struct {
	signature string // Chữ ký số Ed25519 (mã hóa Base64)
	tsRaw     string // Mã timestamp gửi lên dạng chuỗi (Unix timestamp)
	nonce     string // Mã ngẫu nhiên sử dụng một lần (Nonce)
}

// ============================================================================
// 🔒 TRẠNG THÁI RUNTIME TOÀN CỤC (GLOBAL RUNTIME STATE)
// ============================================================================
var sigState = struct {
	mu      sync.RWMutex
	runtime sigRuntime
}{}

// InitAdminCriticalSignature khởi tạo runtime cấu hình cho critical signature guard.
// Được gọi một lần duy nhất tại bootstrap để wire các dependency (Loader, Redis, TTL, Skew).
func InitAdminCriticalSignature(
	cacheEngine *cacheengine.CacheRegistry,
	rds *goredis.Client,
	nonceTTL time.Duration,
	skew time.Duration,
) error {
	// Kiểm tra tính hợp lệ của các đầu vào bắt buộc:
	if cacheEngine == nil {
		return errors.New("admin critical signature: cache engine is required")
	}
	if rds == nil {
		return errors.New("admin critical signature: redis client is required")
	}
	if nonceTTL <= 0 {
		return errors.New("admin critical signature: nonce ttl must be positive")
	}
	if skew <= 0 {
		return errors.New("admin critical signature: skew must be positive")
	}

	sigState.mu.Lock()
	sigState.runtime = sigRuntime{
		cacheEngine: cacheEngine,
		rds:         rds,
		nonceTTL:    nonceTTL,
		skew:        skew,
	}
	sigState.mu.Unlock()
	return nil
}

// ============================================================================
// 🛡️ MIDDLEWARE: ADMIN CRITICAL SIGNATURE (XÁC THỰC CHỮ KÝ SỐ ED25519)
// ============================================================================

// AdminCriticalSignature áp dụng xác thực chữ ký số mật mã Ed25519 cho các hành động Admin nhạy cảm.
//
// 🎯 YÊU CẦU THỨ TỰ (CHAINING CONTRACT):
//   - Middleware này BẮT BUỘC phải chạy SAU middleware xác thực chính (ví dụ: AdminAPIKeyAuth).
//   - Nhằm đảm bảo `accessKey` đã được kiểm chứng và inject thành công vào gin.Context.
func AdminCriticalSignature() gin.HandlerFunc {
	return func(c *gin.Context) {
		runtime := getSigRuntime()
		// Kiểm tra hệ thống đã được cấu hình runtime đầy đủ chưa:
		if runtime.cacheEngine == nil || runtime.rds == nil {
			sigUnavailable(c)
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Đọc bằng chứng chữ ký (Signature, Timestamp, Nonce) từ Headers
		//   và kiểm tra Timestamp chống trễ giờ hoặc sửa giờ (clock skew).
		// --------------------------------------------------------------------
		proof, ok := readSigProof(c, runtime.skew)
		if !ok {
			denySig(c)
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Lấy `accessKey` của Admin từ context (được inject ở auth middleware).
		// --------------------------------------------------------------------
		accessKey, ok := readAccessKey(c)
		if !ok {
			denySig(c)
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Đọc body, tính mã băm SHA256(Body), rồi khôi phục lại Body 
		//   để handler phía sau vẫn đọc được bình thường.
		// --------------------------------------------------------------------
		bodyHash, ok := readSigBody(c)
		if !ok {
			denySig(c)
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Gọi loader lấy khóa công khai Ed25519 của thiết bị Admin 
		//   tương ứng với accessKey qua cacheEngine.
		// --------------------------------------------------------------------
		pubKeyVal, err := runtime.cacheEngine.GetOrLoad(c.Request.Context(), "admin_public_key", accessKey)
		if err != nil || pubKeyVal == nil {
			denySig(c)
			return
		}
		pubKeyRaw, ok := pubKeyVal.(string)
		if !ok {
			denySig(c)
			return
		}
		pubKeyBytes, ok := decodeSigB64(strings.TrimSpace(pubKeyRaw), ed25519.PublicKeySize)
		if !ok {
			denySig(c)
			return
		}
		pubKey := ed25519.PublicKey(pubKeyBytes)

		// --------------------------------------------------------------------
		// 🔄 Xây dựng payload ký chuẩn hóa (Canonical Payload) 
		//   và gọi ed25519.Verify để xác thực chữ ký.
		// --------------------------------------------------------------------
		payload := buildSigPayload(c, bodyHash, proof, accessKey)
		if !verifySig(pubKey, payload, proof.signature) {
			denySig(c)
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Ghi nhận Nonce vào Redis bằng SETNX để khóa và chống 
		//   phát lại (Replay) vĩnh viễn trong thời gian TTL.
		// --------------------------------------------------------------------
		reserved, err := reserveSigNonce(c.Request.Context(), runtime.rds, runtime.nonceTTL, accessKey, proof.nonce)
		if err != nil {
			sigUnavailable(c)
			return
		}
		if !reserved {
			// Nonce này đã từng được sử dụng trước đó -> Chặn tấn công Replay!
			denySig(c)
			return
		}

		// Mọi bước xác thực mật mã thành công -> Cho phép xử lý nghiệp vụ
		c.Next()
	}
}

// ============================================================================
// 🛠️ CÁC HÀM TRỢ GIÚP NỘI BỘ (HELPER FUNCTIONS)
// ============================================================================

// getSigRuntime lấy bản sao cấu hình runtime chữ ký một cách an toàn luồng.
func getSigRuntime() sigRuntime {
	sigState.mu.RLock()
	runtime := sigState.runtime
	sigState.mu.RUnlock()
	return runtime
}

// readSigProof đọc các header chữ ký số và kiểm tra giới hạn sai lệch timestamp (Clock Skew).
func readSigProof(c *gin.Context, skew time.Duration) (sigProof, bool) {
	proof := sigProof{
		signature: strings.TrimSpace(c.GetHeader(constant.HeaderAdminSignature)),
		tsRaw:     strings.TrimSpace(c.GetHeader(constant.HeaderAdminTimestamp)),
		nonce:     strings.TrimSpace(c.GetHeader(constant.HeaderAdminNonce)),
	}
	
	// Tất cả 3 thông số bắt buộc phải được gửi kèm:
	if proof.signature == "" || proof.tsRaw == "" || proof.nonce == "" {
		return sigProof{}, false
	}

	// Chuyển đổi timestamp dạng chuỗi sang số nguyên:
	tsUnix, err := strconv.ParseInt(proof.tsRaw, 10, 64)
	if err != nil {
		return sigProof{}, false
	}
	
	// So sánh độ lệch thời gian với đồng hồ Server:
	ts := time.Unix(tsUnix, 0).UTC()
	now := time.Now().UTC()
	if now.Sub(ts) > skew || ts.Sub(now) > skew {
		// Tránh việc kẻ tấn công lấy một chữ ký hợp lệ trong quá khứ để gửi lại sau nhiều giờ/ngày
		return sigProof{}, false
	}

	return proof, true
}

// readAccessKey lấy AccessKey đã được xác thực từ tầng xác thực chính trước đó.
func readAccessKey(c *gin.Context) (string, bool) {
	raw, exists := c.Get(constant.ContextKeyAdminAccessKey)
	if !exists {
		return "", false
	}
	accessKey, ok := raw.(string)
	accessKey = strings.TrimSpace(accessKey)
	return accessKey, ok && accessKey != ""
}

// readSigBody đọc toàn bộ luồng byte của Body, tính toán băm SHA256
// và khôi phục lại `io.ReadCloser` gốc để handler phía sau vẫn đọc được nội dung Body bình thường.
func readSigBody(c *gin.Context) ([sha256.Size]byte, bool) {
	// 🎯 TỐI ƯU 1: Nếu Body rỗng hoặc không tồn tại (GET, DELETE,...) -> Trả về mã băm rỗng ngay lập tức
	// Tránh việc cấp phát vùng nhớ RAM (allocation) và đọc stream vô nghĩa.
	if c.Request.Body == nil || c.Request.Body == http.NoBody {
		return sha256.Sum256(nil), true
	}

	// 🎯 TỐI ƯU 2: Bảo mật chống tấn công cạn kiệt bộ nhớ (OOM Attack).
	// Các endpoint hành động Admin (critical admin actions) chỉ gửi các payload JSON cấu hình rất nhỏ (thường < 100KB).
	// Việc giới hạn 2MB bằng io.LimitReader bảo vệ Server khỏi bị cạn kiệt RAM nếu kẻ xấu cố tình upload file dung lượng lớn.
	const maxAdminBodyLimit = 2 * 1024 * 1024 // 2MB
	limitedReader := io.LimitReader(c.Request.Body, maxAdminBodyLimit)

	bodyRaw, err := io.ReadAll(limitedReader)
	if err != nil {
		return [sha256.Size]byte{}, false
	}

	// Khôi phục stream Body bằng cách tạo một Reader mới từ mảng byte đã đọc:
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyRaw))

	// Trả về mã băm SHA256 của body để phục vụ build canonical payload:
	return sha256.Sum256(bodyRaw), true
}



// buildSigPayload xây dựng chuỗi Payload chuẩn hóa (Canonical String) để thực hiện xác thực chữ ký số.
// Định dạng chuỗi payload:
// HTTP_METHOD + "\n" + PATH + "\n" + RAW_QUERY + "\n" + HEX(SHA256_BODY) + "\n" + TIMESTAMP + "\n" + NONCE
func buildSigPayload(
	c *gin.Context,
	bodyHash [sha256.Size]byte,
	proof sigProof,
	_ string,
) string {
	return fmt.Sprintf("%s\n%s\n%s\n%x\n%s\n%s",
		strings.ToUpper(c.Request.Method), // Phương thức HTTP (ví dụ: POST)
		c.Request.URL.Path,                // Đường dẫn API (ví dụ: /admin/core/zones)
		c.Request.URL.RawQuery,            // Query string thô (nếu có)
		bodyHash,                          // Mã băm SHA256 của Body dưới dạng hex string
		proof.tsRaw,                       // Timestamp gửi lên
		proof.nonce,                       // Nonce gửi lên
	)
}

// verifySig thực hiện giải mã chữ ký Base64 và gọi hàm mật mã ed25519.Verify để đối chiếu.
func verifySig(pubKey ed25519.PublicKey, payload string, sigRaw string) bool {
	signature, ok := decodeSigB64(strings.TrimSpace(sigRaw), -1)
	if !ok {
		return false
	}
	return ed25519.Verify(pubKey, []byte(payload), signature)
}

// decodeSigB64 thực hiện giải mã chuỗi Base64 hỗ trợ cả chuẩn Standard và Raw Base64.
func decodeSigB64(value string, expectedSize int) ([]byte, bool) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil {
		return nil, false
	}
	if expectedSize >= 0 && len(decoded) != expectedSize {
		return nil, false
	}
	return decoded, true
}

// reserveSigNonce thực hiện lưu khóa Nonce chống phát lại lên Redis một cách nguyên tử (Atomic).
// Bằng cách sử dụng lệnh SETNX, chỉ có request đầu tiên với Nonce đó được phép đi qua, các request trùng Nonce tiếp theo sẽ bị từ chối ngay.
func reserveSigNonce(ctx context.Context, rds *goredis.Client, ttl time.Duration, accessKey string, nonce string) (bool, error) {
	nonceKey := sigNoncePrefix + strings.TrimSpace(accessKey) + ":" + strings.TrimSpace(nonce)
	return rds.SetNX(ctx, nonceKey, "1", ttl).Result()
}

// denySig ngắt kết nối và trả về lỗi 401 Unauthorized khi chữ ký không hợp lệ.
func denySig(c *gin.Context) {
	apires.RespondUnauthorized(c, "unauthorized")
	c.Abort()
}

// sigUnavailable ngắt kết nối và trả về lỗi 503 Service Unavailable khi các thành phần hỗ trợ bảo mật lỗi.
func sigUnavailable(c *gin.Context) {
	apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
	c.Abort()
}
