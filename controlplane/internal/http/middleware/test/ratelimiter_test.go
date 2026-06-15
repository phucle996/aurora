package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"controlplane/pkg/constant"
	middleware "controlplane/internal/http/middleware"
	policyRateLimit "controlplane/internal/policyengine/policies/ratelimit"
	"controlplane/internal/security/ratelimit"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTestRedis(t *testing.T) (*redis.Client, func()) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	return client, func() {
		client.Close()
		mr.Close()
	}
}

func TestRateLimitPostAuth_NoFallbackAndBypass(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	bucket := ratelimit.NewBucket(client)
	bucket.SetFailOpen(false)

	// Khởi tạo chính sách không có bypass route và không có rules
	policy := policyRateLimit.CompiledPolicy{
		Behavior: policyRateLimit.CompiledRateLimitBehaviorPolicy{
			BypassRoutePatterns: []string{},
		},
		PostAuth: policyRateLimit.CompiledRateLimitPostAuthPolicy{
			Rules: []policyRateLimit.CompiledRateLimitPathRule{},
		},
	}
	middleware.InitRateLimitPolicy(policy)

	// Khởi tạo router Gin
	r := gin.New()
	path := "/api/v1/secure-action"
	r.POST(path, middleware.RateLimitPostAuth(bucket, path), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Không có rule nào được cấu hình -> Phải được bypass và cho phép ngay lập tức (Zero Fallback)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", path, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK for missing policy bypass, got %d", w.Code)
	}
}

func TestRateLimitPostAuth_EnforcementAndRedisKeys(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	bucket := ratelimit.NewBucket(client)
	bucket.SetFailOpen(false)

	path := "/api/v1/auth/login"

	// Cấu hình rule cho path với capacity là 2 yêu cầu, nạp lại 2 yêu cầu mỗi phút
	policy := policyRateLimit.CompiledPolicy{
		Behavior: policyRateLimit.CompiledRateLimitBehaviorPolicy{},
		PostAuth: policyRateLimit.CompiledRateLimitPostAuthPolicy{
			Rules: []policyRateLimit.CompiledRateLimitPathRule{
				{
					Path:          path,
					Capacity:      2,
					Refill:        2,
					PeriodSeconds: 60,
				},
			},
		},
	}
	middleware.InitRateLimitPolicy(policy)

	r := gin.New()
	// Middleware access guard giả lập để inject user identity sử dụng Identity struct chuẩn
	r.Use(func(c *gin.Context) {
		ident := &constant.Identity{
			UserID:    "user-123",
			AccessKey: "device-abc",
		}
		ctx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.POST(path, middleware.RateLimitPostAuth(bucket, path), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Gửi yêu cầu 1 -> Success
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", path, nil)
	req1.RemoteAddr = "192.168.1.1:1234"
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Errorf("request 1 failed: %d", w1.Code)
	}

	// Gửi yêu cầu 2 -> Success
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", path, nil)
	req2.RemoteAddr = "192.168.1.1:1234"
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("request 2 failed: %d", w2.Code)
	}

	// Gửi yêu cầu 3 -> Blocked (429 Too Many Requests)
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("POST", path, nil)
	req3.RemoteAddr = "192.168.1.1:1234"
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 Too Many Requests for request 3, got %d", w3.Code)
	}

	// Kiểm tra tính hợp lệ của Redis key namespace
	ctx := context.Background()
	keys, err := client.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatalf("failed to query keys from redis: %v", err)
	}

	expectedPrefix := path + ":ip_device:"
	foundExpectedKey := false
	for _, k := range keys {
		if len(k) >= len(expectedPrefix) && k[:len(expectedPrefix)] == expectedPrefix {
			foundExpectedKey = true
			break
		}
	}

	if !foundExpectedKey {
		t.Errorf("expected to find a Redis key starting with '%s', got keys: %v", expectedPrefix, keys)
	}
}

func TestRateLimitPostAuth_HotReload(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	bucket := ratelimit.NewBucket(client)
	bucket.SetFailOpen(false)

	path := "/api/v1/templates"

	// Cấu hình ban đầu: chỉ cho phép 1 yêu cầu
	policy1 := policyRateLimit.CompiledPolicy{
		PostAuth: policyRateLimit.CompiledRateLimitPostAuthPolicy{
			Rules: []policyRateLimit.CompiledRateLimitPathRule{
				{
					Path:          path,
					Capacity:      1,
					Refill:        1,
					PeriodSeconds: 60,
				},
			},
		},
	}
	middleware.InitRateLimitPolicy(policy1)

	r := gin.New()
	r.POST(path, middleware.RateLimitPostAuth(bucket, path), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Yêu cầu 1 -> OK
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", path, nil)
	req1.RemoteAddr = "192.168.1.2:1234"
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Errorf("expected OK, got %d", w1.Code)
	}

	// Yêu cầu 2 -> Blocked
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", path, nil)
	req2.RemoteAddr = "192.168.1.2:1234"
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w2.Code)
	}

	// Hot reload chính sách nâng cấp capacity lên 5
	policy2 := policyRateLimit.CompiledPolicy{
		PostAuth: policyRateLimit.CompiledRateLimitPostAuthPolicy{
			Rules: []policyRateLimit.CompiledRateLimitPathRule{
				{
					Path:          path,
					Capacity:      5,
					Refill:        5,
					PeriodSeconds: 60,
				},
			},
		},
	}
	middleware.InitRateLimitPolicy(policy2)

	// Do đã nâng cấp capacity lên 5, yêu cầu mới với IP khác sẽ được cho phép thành công lập tức
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("POST", path, nil)
	req3.RemoteAddr = "192.168.1.3:1234"
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("expected 200 OK after dynamic hot-reload, got %d", w3.Code)
	}
}
