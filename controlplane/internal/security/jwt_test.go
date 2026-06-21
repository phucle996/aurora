package security

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

// [COMMENT]: TestJWTBasicSignAndParse kiểm tra quy trình ký và giải mã JWT cơ bản sử dụng fallback secret.
func TestJWTBasicSignAndParse(t *testing.T) {
	claims := Claims{
		Subject:   "user-123",
		Level:     1,
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}

	token, err := SignWithSecret(claims, nil)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	parsedClaims, err := Parse(token, nil)
	if err != nil {
		t.Fatalf("failed to parse token: %v", err)
	}

	if parsedClaims.Subject != claims.Subject {
		t.Errorf("expected subject %q, got %q", claims.Subject, parsedClaims.Subject)
	}
}

// [COMMENT]: TestJWTVerificationCache kiểm tra tính năng L1 Cache của Signature Verification.
func TestJWTVerificationCache(t *testing.T) {
	sig := "test-signature-part"
	sigHashBytes := sha256.Sum256([]byte(sig))
	sigHash := base64.RawURLEncoding.EncodeToString(sigHashBytes[:])

	// Xóa sạch cache trước khi test
	verifyCache.mu.Lock()
	verifyCache.store = make(map[string]signatureCacheEntry)
	verifyCache.mu.Unlock()

	// 1. Kiểm tra cache miss ban đầu
	if _, cached := verifyCache.get(sigHash); cached {
		t.Fatal("expected cache miss for new signature")
	}

	// 2. Set cache với trạng thái hợp lệ và kiểm tra cache hit
	verifyCache.set(sigHash, true, 50*time.Millisecond)
	valid, cached := verifyCache.get(sigHash)
	if !cached {
		t.Fatal("expected cache hit after setting key")
	}
	if !valid {
		t.Fatal("expected cached verification to be valid")
	}

	// 3. Đợi cache hết hạn (TTL) và kiểm tra cache miss trở lại
	time.Sleep(60 * time.Millisecond)
	if _, cached := verifyCache.get(sigHash); cached {
		t.Fatal("expected cache miss after TTL expiration")
	}

	// 4. Set cache với trạng thái không hợp lệ
	verifyCache.set(sigHash, false, 50*time.Millisecond)
	valid, cached = verifyCache.get(sigHash)
	if !cached {
		t.Fatal("expected cache hit for invalid status")
	}
	if valid {
		t.Fatal("expected cached verification to be invalid")
	}
}

// [COMMENT]: TestJWTVerificationCacheLazyCleanup kiểm tra tính năng lazy cleanup của verifyCache.
func TestJWTVerificationCacheLazyCleanup(t *testing.T) {
	// Xóa sạch cache trước khi test
	verifyCache.mu.Lock()
	verifyCache.store = make(map[string]signatureCacheEntry)
	verifyCache.mu.Unlock()

	// Khởi hoạt 1005 bản ghi. 500 bản ghi đầu đã hết hạn, 505 bản ghi sau chưa hết hạn.
	for i := 0; i < 1005; i++ {
		sig := fmt.Sprintf("sig-%d", i)
		hashBytes := sha256.Sum256([]byte(sig))
		hash := base64.RawURLEncoding.EncodeToString(hashBytes[:])

		ttl := 1 * time.Hour
		if i < 500 {
			ttl = -1 * time.Second
		}
		verifyCache.set(hash, true, ttl)
	}

	// Tại thời điểm này, vì size vượt quá 1000 trong quá trình set (tại i = 1001),
	// lazy cleanup đã tự động được kích hoạt và xóa bỏ 500 bản ghi đã hết hạn.
	// Do đó, tổng số bản ghi còn lại phải nhỏ hơn hoặc bằng 506.
	verifyCache.mu.Lock()
	size := len(verifyCache.store)
	verifyCache.mu.Unlock()

	if size > 600 {
		t.Errorf("expected lazy cleanup to prune expired entries, but store size is %d", size)
	}
}
