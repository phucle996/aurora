// ============================================================================
// 🛡️ ADMIN CRITICAL SIGNATURE — Ed25519 Request Signing Guard
// ============================================================================
//
// 🤝 CONTRACT:
//   - Chạy SAU AdminAPIKeyAuth để đảm bảo `ContextKeyAdminAccessKey` đã được inject.
//   - Xác thực chữ ký Ed25519 trên canonical payload: METHOD\nPATH\nQUERY\nSHA256(BODY)\nTS\nNONCE.
//   - Nonce được khoá nguyên tử qua Lua SetNX trên CacheEngine L2 để chống Replay.
//   - Fail-Closed: thiếu runtime hoặc lỗi cache → 503.
//
// 💡 DEPENDENCIES:
//   - CacheRegistry.GetOrLoad("admin_public_key", accessKey) → Ed25519 pubkey (base64).
//   - CacheRegistry.Exec.Execute (Lua) → atomic nonce reservation.

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
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
)

const (
	sigNoncePrefix       = "iam:admin:critical:nonce:"
	sigMaxBodySize int64 = 2 << 20 // 2 MB
)

// ============================================================================
// GLOBAL RUNTIME STATE
// ============================================================================

type sigRuntime struct {
	cacheEngine *cacheengine.CacheRegistry
	nonceTTL    time.Duration
	skew        time.Duration
}

var sigState = struct {
	mu      sync.RWMutex
	runtime sigRuntime
}{}

// InitAdminCriticalSignature wire CacheRegistry duy nhất, bỏ raw rdb.
func InitAdminCriticalSignature(
	cacheEngine *cacheengine.CacheRegistry,
	nonceTTL time.Duration,
	skew time.Duration,
) error {
	if cacheEngine == nil {
		return errors.New("admin critical signature: cache engine is required")
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
		nonceTTL:    nonceTTL,
		skew:        skew,
	}
	sigState.mu.Unlock()
	return nil
}

// ============================================================================
// MIDDLEWARE
// ============================================================================

func AdminCriticalSignature() gin.HandlerFunc {
	return func(c *gin.Context) {
		sigState.mu.RLock()
		rt := sigState.runtime
		sigState.mu.RUnlock()

		// 1. Đọc và kiểm tra Timestamp, Signature, Nonce từ header.
		sig := strings.TrimSpace(c.GetHeader(constant.HeaderAdminSignature))
		tsRaw := strings.TrimSpace(c.GetHeader(constant.HeaderAdminTimestamp))
		nonce := strings.TrimSpace(c.GetHeader(constant.HeaderAdminNonce))
		if sig == "" || tsRaw == "" || nonce == "" {
			denySig(c)
			return
		}
		tsUnix, err := strconv.ParseInt(tsRaw, 10, 64)
		if err != nil {
			denySig(c)
			return
		}
		ts := time.Unix(tsUnix, 0).UTC()
		now := time.Now().UTC()
		delta := ts.Sub(now)
		if delta < -rt.skew || delta > rt.skew {
			denySig(c)
			return
		}

		// 2. Lấy accessKey từ context (đã inject bởi AdminAPIKeyAuth).
		rawKey, _ := c.Get(constant.ContextKeyAdminAccessKey)
		accessKey, _ := rawKey.(string)
		accessKey = strings.TrimSpace(accessKey)
		if accessKey == "" {
			denySig(c)
			return
		}

		// 3. Đọc body → SHA256, khôi phục body stream cho handler sau.
		bodyHash, ok := sigReadBody(c)
		if !ok {
			denySig(c)
			return
		}

		// 4. Lấy Ed25519 public key từ CacheRegistry (L1/L2/DB qua loader).
		pubKeyVal, err := rt.cacheEngine.GetOrLoad(c.Request.Context(), "admin_public_key", accessKey)
		if err != nil || pubKeyVal == nil {
			denySig(c)
			return
		}
		pubKeyStr, ok := pubKeyVal.(string)
		if !ok || strings.TrimSpace(pubKeyStr) == "" {
			denySig(c)
			return
		}
		pubKeyBytes, ok := sigDecodeB64(strings.TrimSpace(pubKeyStr), ed25519.PublicKeySize)
		if !ok {
			denySig(c)
			return
		}

		// 5. Xây dựng canonical payload và xác thực chữ ký.
		payload := fmt.Sprintf("%s\n%s\n%s\n%x\n%s\n%s",
			strings.ToUpper(c.Request.Method),
			c.Request.URL.Path,
			c.Request.URL.RawQuery,
			bodyHash,
			tsRaw,
			nonce,
		)
		sigBytes, ok := sigDecodeB64(sig, -1)
		if !ok || !ed25519.Verify(ed25519.PublicKey(pubKeyBytes), []byte(payload), sigBytes) {
			denySig(c)
			return
		}

		// 6. Atomic nonce reservation qua Lua SetNX trên CacheEngine L2.
		nonceKey := sigNoncePrefix + accessKey + ":" + nonce
		luaSetNX := `
if redis.call("set", KEYS[1], "1", "NX", "PX", ARGV[1]) then
  return 1
else
  return 0
end`
		ttlMs := int64(rt.nonceTTL / time.Millisecond)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
		defer cancel()

		res, execErr := rt.cacheEngine.Exec.Execute(ctx, luaSetNX, []string{nonceKey}, ttlMs)
		if execErr != nil {
			sigUnavailable(c)
			return
		}
		if v, _ := res.(int64); v != 1 {
			// Nonce đã tồn tại → replay attack.
			denySig(c)
			return
		}

		c.Next()
	}
}

// ============================================================================
// HELPERS
// ============================================================================

// sigReadBody đọc body (tối đa 2MB), tính SHA256 và khôi phục stream.
func sigReadBody(c *gin.Context) ([sha256.Size]byte, bool) {
	if c.Request.Body == nil || c.Request.Body == http.NoBody {
		return sha256.Sum256(nil), true
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, sigMaxBodySize))
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	return sha256.Sum256(raw), true
}

// sigDecodeB64 giải mã base64 (standard hoặc raw), kiểm tra kích thước nếu expectedSize >= 0.
func sigDecodeB64(value string, expectedSize int) ([]byte, bool) {
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || (expectedSize >= 0 && len(b) != expectedSize) {
		return nil, false
	}
	return b, true
}

func denySig(c *gin.Context) {
	apires.RespondUnauthorized(c, "unauthorized")
	c.Abort()
}

func sigUnavailable(c *gin.Context) {
	apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
	c.Abort()
}
