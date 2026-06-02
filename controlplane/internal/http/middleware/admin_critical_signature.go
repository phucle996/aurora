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
	"strconv"
	"strings"
	"sync"
	"time"

	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
)

const sigNoncePrefix = "iam:admin:critical:nonce:"

type sigRuntime struct {
	loadPubKey func(ctx context.Context, accessKey string) (string, error)
	rds        *goredis.Client
	nonceTTL   time.Duration
	skew       time.Duration
}

type sigProof struct {
	signature string
	tsRaw     string
	nonce     string
}

var sigState = struct {
	mu      sync.RWMutex
	runtime sigRuntime
}{}

// InitAdminCriticalSignature khởi tạo runtime cho critical signature guard.
//
// Source of truth:
// - app/module.go truyền vào function load public key từ admin runtime/session.
// - Middleware không import IAM/Core module, chỉ gọi contract function đã inject.
//
// Runtime contract:
// - nonceTTL là thời gian Redis giữ nonce đã dùng để chống replay.
// - skew là độ lệch thời gian tối đa cho X-Admin-Timestamp.
func InitAdminCriticalSignature(
	loadPubKey func(ctx context.Context, accessKey string) (string, error),
	rds *goredis.Client,
	nonceTTL time.Duration,
	skew time.Duration,
) error {
	if loadPubKey == nil {
		return errors.New("admin critical signature: load access public key is required")
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
		loadPubKey: loadPubKey,
		rds:        rds,
		nonceTTL:   nonceTTL,
		skew:       skew,
	}
	sigState.mu.Unlock()
	return nil
}

// AdminCriticalSignature xác thực chữ ký Ed25519 cho critical action.
//
// Middleware này phải chạy sau AdminAPIKeyAuth(WithInjectAccessKey()), vì accessKey
// trong JWT/cookie đã được auth middleware xác thực và inject vào gin.Context.
//
// Flow intentionally explicit:
// 1. đọc signature/timestamp/nonce headers.
// 2. kiểm tra timestamp nằm trong clock skew cho phép.
// 3. lấy admin access key đã được auth middleware inject.
// 4. đọc body, hash body, rồi restore body để handler phía sau vẫn đọc được.
// 5. load public key từ source-of-truth qua function đã inject.
// 6. verify Ed25519 signature trên canonical payload.
// 7. reserve nonce trong Redis sau khi signature hợp lệ để chống replay.
//
// Performance note:
//   - Middleware không giữ RAM cache riêng để tránh stale key không có invalidation.
//   - Public key lookup được tối ưu ở app wiring bằng Redis admin runtime; fallback
//     DB chỉ dành cho runtime record cũ chưa có public key.
func AdminCriticalSignature() gin.HandlerFunc {
	return func(c *gin.Context) {
		runtime := getSigRuntime()
		if runtime.loadPubKey == nil || runtime.rds == nil {
			sigUnavailable(c)
			return
		}

		proof, ok := readSigProof(c, runtime.skew)
		if !ok {
			denySig(c)
			return
		}

		accessKey, ok := readAccessKey(c)
		if !ok {
			denySig(c)
			return
		}

		bodyHash, ok := readSigBody(c)
		if !ok {
			denySig(c)
			return
		}

		pubKey, ok := loadSigKey(c.Request.Context(), runtime.loadPubKey, accessKey)
		if !ok {
			denySig(c)
			return
		}

		payload := buildSigPayload(c, bodyHash, proof, accessKey)
		if !verifySig(pubKey, payload, proof.signature) {
			denySig(c)
			return
		}

		reserved, err := reserveSigNonce(c.Request.Context(), runtime.rds, runtime.nonceTTL, accessKey, proof.nonce)
		if err != nil {
			sigUnavailable(c)
			return
		}
		if !reserved {
			denySig(c)
			return
		}

		c.Next()
	}
}

func getSigRuntime() sigRuntime {
	sigState.mu.RLock()
	runtime := sigState.runtime
	sigState.mu.RUnlock()
	return runtime
}

func readSigProof(c *gin.Context, skew time.Duration) (sigProof, bool) {
	proof := sigProof{
		signature: strings.TrimSpace(c.GetHeader(constant.HeaderAdminSignature)),
		tsRaw:     strings.TrimSpace(c.GetHeader(constant.HeaderAdminTimestamp)),
		nonce:     strings.TrimSpace(c.GetHeader(constant.HeaderAdminNonce)),
	}
	if proof.signature == "" || proof.tsRaw == "" || proof.nonce == "" {
		return sigProof{}, false
	}

	tsUnix, err := strconv.ParseInt(proof.tsRaw, 10, 64)
	if err != nil {
		return sigProof{}, false
	}
	ts := time.Unix(tsUnix, 0).UTC()
	now := time.Now().UTC()
	if now.Sub(ts) > skew || ts.Sub(now) > skew {
		return sigProof{}, false
	}

	return proof, true
}

func readAccessKey(c *gin.Context) (string, bool) {
	raw, exists := c.Get(constant.ContextKeyAdminAccessKey)
	if !exists {
		return "", false
	}
	accessKey, ok := raw.(string)
	accessKey = strings.TrimSpace(accessKey)
	return accessKey, ok && accessKey != ""
}

func readSigBody(c *gin.Context) ([sha256.Size]byte, bool) {
	var bodyRaw []byte
	if c.Request.Body != nil {
		var err error
		bodyRaw, err = io.ReadAll(c.Request.Body)
		if err != nil {
			return [sha256.Size]byte{}, false
		}
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyRaw))
	return sha256.Sum256(bodyRaw), true
}

func loadSigKey(
	ctx context.Context,
	loadPubKey func(ctx context.Context, accessKey string) (string, error),
	accessKey string,
) (ed25519.PublicKey, bool) {
	pubKeyRaw, err := loadPubKey(ctx, accessKey)
	if err != nil {
		return nil, false
	}
	pubKey, ok := decodeSigB64(strings.TrimSpace(pubKeyRaw), ed25519.PublicKeySize)
	if !ok {
		return nil, false
	}
	return ed25519.PublicKey(pubKey), true
}

func buildSigPayload(
	c *gin.Context,
	bodyHash [sha256.Size]byte,
	proof sigProof,
	// accessKey không được đưa vào payload string vì:
	//   1. Đã được gửi kèm qua HttpOnly cookie — backend tự lấy từ context.
	//   2. Expose accessKey trong payload string gây nguy cơ lộ qua log/trace.
	//   3. Signature đã bind với device thông qua device public key lookup dùng accessKey.
	_ string,
) string {
	return fmt.Sprintf("%s\n%s\n%s\n%x\n%s\n%s",
		strings.ToUpper(c.Request.Method),
		c.Request.URL.Path,
		c.Request.URL.RawQuery,
		bodyHash,
		proof.tsRaw,
		proof.nonce,
	)
}

func verifySig(pubKey ed25519.PublicKey, payload string, sigRaw string) bool {
	signature, ok := decodeSigB64(strings.TrimSpace(sigRaw), -1)
	if !ok {
		return false
	}
	return ed25519.Verify(pubKey, []byte(payload), signature)
}

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

func reserveSigNonce(ctx context.Context, rds *goredis.Client, ttl time.Duration, accessKey string, nonce string) (bool, error) {
	nonceKey := sigNoncePrefix + strings.TrimSpace(accessKey) + ":" + strings.TrimSpace(nonce)
	return rds.SetNX(ctx, nonceKey, "1", ttl).Result()
}

func denySig(c *gin.Context) {
	apires.RespondUnauthorized(c, "unauthorized")
	c.Abort()
}

func sigUnavailable(c *gin.Context) {
	apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
	c.Abort()
}
