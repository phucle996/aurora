package middleware

import (
	policyRateLimit "controlplane/internal/policyengine/policies/ratelimit"
	"controlplane/internal/security/ratelimit"
	"controlplane/pkg/apires"
	"controlplane/pkg/logger"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	middleware_metrics "controlplane/internal/http/middleware/metrics"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// 📌 HẰNG SỐ CẤU HÌNH PHẠM VI & KẾT QUẢ RATE LIMIT (CONSTANTS)
// ============================================================================
const (
	// Các phạm vi kiểm tra (Scope) dựa trên mức độ nhận dạng Client:
	rateLimitScopeIP         = "ip"          // Chỉ nhận diện qua Client IP (Thô - PreAuth)
	rateLimitScopeIPTracking = "ip_tracking" // Nhận diện qua IP kết hợp Device ID (Mịn - PostAuth)
	rateLimitScopeIPUser     = "ip_user"     // Nhận diện qua IP kết hợp User ID (Mịn - PostAuth)

	// Các kết quả đánh giá kỹ thuật (Result):
	rateLimitResultAllow   = "allow"   // Được phép đi qua
	rateLimitResultBlocked = "blocked" // Bị chặn do vượt quota hoặc nằm trong blacklist
	rateLimitResultBypass  = "bypass"  // Bỏ qua không kiểm tra (Ví dụ: health check)
	rateLimitResultError   = "error"   // Lỗi hệ thống trong quá trình check (Ví dụ: Redis die)

	// Các quyết định kiểm soát và leo thang hành vi (Decision/Escalation):
	rateLimitDecisionThrottle  = "throttle"            // Giảm tốc / Chậm lại (Trả về 429)
	rateLimitDecisionIsolation = "temporary_isolation" // Cách ly tạm thời đối tượng nghi vấn
	rateLimitDecisionBlock     = "block"               // Khóa hoàn toàn truy cập
)

// ============================================================================
// 🔄 ĐỘNG CƠ QUYẾT ĐỊNH & LƯU TRỮ CẤU HÌNH ĐỘNG (ENGINE & STORAGE)
// ============================================================================

// rateLimitEngine là Decision Engine cục bộ, quản lý danh sách đen/cách ly tạm thời (Local Deny Cache).
// Giúp tránh phải truy vấn Redis liên tục đối với các IP/User đã được xác định là đang spam.
var rateLimitEngine = ratelimit.NewDecisionEngine()

// rateLimitPolicyConfig lưu trữ cấu hình rate limit đã biên dịch và sẵn sàng thực thi ở runtime.
type rateLimitPolicyConfig struct {
	preAuthCapacity          int64                                                    // Sức chứa của bucket PreAuth (IP-based)
	preAuthRefill            int64                                                    // Tốc độ nạp lại của bucket PreAuth
	preAuthPeriod            time.Duration                                            // Chu kỳ thời gian của bucket PreAuth
	postAuthPathRules        map[string]policyRateLimit.CompiledRateLimitBucketPolicy // Map chứa quy tắc rate limit riêng cho từng Path (Post-Auth)
	globalInstantMaxInflight int64                                                    // Số lượng request đồng thời tối đa toàn hệ thống (Inflight limit)
	globalInstantRetryAfter  time.Duration                                            // Thời gian client phải chờ khi chạm ngưỡng MaxInflight
	retryFallback            time.Duration                                            // Thời gian chờ mặc định khi Redis không trả về RetryAfter
	throttleSample           int                                                      // Tỷ lệ log sampling cho hành vi Throttle (%)
	isolationSample          int                                                      // Tỷ lệ log sampling cho hành vi Isolation (%)
	blockSample              int                                                      // Tỷ lệ log sampling cho hành vi Block (%)
	errorSample              int                                                      // Tỷ lệ log sampling cho hành vi Error (%)
	bypassRoutes             map[string]struct{}                                      // Tập hợp các route được bypass hoàn toàn
}

// Cấu hình Rate Limit được lưu trữ trong một atomic.Value để đảm bảo an toàn luồng (Thread-safety).
var rateLimitPolicyHolder atomic.Value

// rateLimitInflight theo dõi số lượng request hiện tại đang nằm trong pipeline xử lý của limiter.
var rateLimitInflight atomic.Int64

// currentRateLimitPolicy là hàm helper lấy ra bản sao cấu hình rate limit hiện tại một cách an toàn luồng.
func currentRateLimitPolicy() rateLimitPolicyConfig {
	v := rateLimitPolicyHolder.Load()
	if v == nil {
		return rateLimitPolicyConfig{}
	}
	return v.(rateLimitPolicyConfig)
}

// InitRateLimitPolicy nhận cấu hình đã biên dịch từ Policy Engine, chuyển đổi kiểu dữ liệu thích hợp
// và nạp vào atomic holder để các middleware sử dụng ngay lập tức mà không cần khởi động lại ứng dụng.
func InitRateLimitPolicy(policy policyRateLimit.CompiledPolicy) {
	// Khởi tạo map cho các route bypass để kiểm tra O(1):
	bypassRoutes := map[string]struct{}{}
	for _, route := range policy.Behavior.BypassRoutePatterns {
		bypassRoutes[route] = struct{}{}
	}

	// Biên dịch các luật giới hạn theo từng đường dẫn (Path Rules):
	pathRules := make(map[string]policyRateLimit.CompiledRateLimitBucketPolicy, len(policy.PostAuth.Rules))
	for _, rule := range policy.PostAuth.Rules {
		pathRules[rule.Path] = policyRateLimit.CompiledRateLimitBucketPolicy{
			Capacity:      rule.Capacity,
			Refill:        rule.Refill,
			PeriodSeconds: rule.PeriodSeconds,
		}
	}

	// Tạo cấu trúc config runtime mới:
	cfg := rateLimitPolicyConfig{
		preAuthCapacity:          policy.PreAuth.IP.Capacity,
		preAuthRefill:            policy.PreAuth.IP.Refill,
		preAuthPeriod:            time.Duration(policy.PreAuth.IP.PeriodSeconds) * time.Second,
		postAuthPathRules:        pathRules,
		globalInstantMaxInflight: policy.PreAuth.GlobalInstant.MaxInflight,
		globalInstantRetryAfter:  time.Duration(policy.PreAuth.GlobalInstant.RetryAfterSeconds) * time.Second,
		retryFallback:            time.Duration(policy.Behavior.RetryAfterFallbackSeconds) * time.Second,
		throttleSample:           policy.Observability.SamplingPercent.Throttle,
		isolationSample:          policy.Observability.SamplingPercent.TemporaryIsolation,
		blockSample:              policy.Observability.SamplingPercent.Block,
		errorSample:              policy.Observability.SamplingPercent.Error,
		bypassRoutes:             bypassRoutes,
	}
	// Ghi đè cấu hình cũ một cách an toàn luồng (Atomic Store):
	rateLimitPolicyHolder.Store(cfg)
}

// ============================================================================
// 🛡️ MIDDLEWARE 1: RATE LIMIT PRE-AUTH (ADMISSION FILTER)
// ============================================================================

// RateLimitPreAuth xử lý lọc chặn thô dựa trên IP trước khi định danh người dùng.
// Nhiệm vụ chính: Bảo vệ tài nguyên tính toán ở rìa hệ thống, ngăn chặn DDoS/Spam thô bạo.
func RateLimitPreAuth(limiter *ratelimit.Bucket, name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// --------------------------------------------------------------------
		// 🚀 BƯỚC B1: LẤY ROUTE PATTERN & KIỂM TRA DANH SÁCH BYPASS (SHORT-CIRCUIT)
		// --------------------------------------------------------------------
		routePattern := routePatternOf(c)
		if shouldBypassRateLimit(routePattern) {
			// Ghi nhận metric và cho qua ngay (ví dụ: các API health check, Prometheus scrape)
			middleware_metrics.RecordRLCheck(routePattern, rateLimitScopeIP, rateLimitResultBypass)
			c.Next()
			return
		}

		// --------------------------------------------------------------------
		// ⚡ BƯỚC B1.2: KIỂM TRA GIỚI HẠN REQUEST ĐỒNG THỜI TOÀN HỆ THỐNG (GLOBAL INFLIGHT)
		// --------------------------------------------------------------------
		if !acquireGlobalInstantPermit() {
			// Hệ thống đang bị quá tải số lượng request đồng thời -> Từ chối ngay lập tức để tự bảo vệ
			retryAfter := currentRateLimitPolicy().globalInstantRetryAfter
			c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(retryAfter))
			middleware_metrics.RecordRLCheck(routePattern, rateLimitScopeIP, rateLimitResultBlocked)
			middleware_metrics.RecordRLDecision(routePattern, rateLimitDecisionThrottle, rateLimitScopeIP)
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, rateLimitDecisionThrottle, "global_instant_cap", retryAfter, 0, "global")
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}
		defer releaseGlobalInstantPermit() // Giải phóng slot inflight khi request xử lý xong

		// --------------------------------------------------------------------
		// 🚀 BƯỚC B2: KIỂM TRA TÍNH HỢP LỆ CỦA CẤU HÌNH (FAIL-OPEN LOCAL)
		// --------------------------------------------------------------------
		policyCfg := currentRateLimitPolicy()
		if limiter == nil || name == "" || policyCfg.preAuthCapacity <= 0 || policyCfg.preAuthRefill <= 0 || policyCfg.preAuthPeriod <= 0 {
			// Cấu hình không hợp lệ hoặc Redis Limiter chưa sẵn sàng -> Cho qua (Fail-Open) để tránh nghẽn luồng nghiệp vụ
			middleware_metrics.RecordRLCheck(routePattern, rateLimitScopeIP, rateLimitResultAllow)
			c.Next()
			return
		}

		// --------------------------------------------------------------------
		// 🚀 BƯỚC B3: XÁC ĐỊNH IDENTITY CỦA CLIENT & TẠO REDIS KEY
		// --------------------------------------------------------------------
		key := ratelimit.Key("", name, clientIdentity(c))
		if key == "" {
			// Không xác định được IP client -> Cho qua để tránh block oan
			middleware_metrics.RecordRLCheck(routePattern, rateLimitScopeIP, rateLimitResultAllow)
			c.Next()
			return
		}

		// --------------------------------------------------------------------
		// 🚀 BƯỚC B4 & B5: TRA CỨU TRẠNG THÁI CHẶN TRONG LOCAL DENY CACHE (FAST-PATH)
		// --------------------------------------------------------------------
		// Kiểm tra trạng thái Throttle cục bộ:
		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionThrottle {
			middleware_metrics.RecordRLCheck(routePattern, rateLimitScopeIP, rateLimitResultBlocked)
			middleware_metrics.RecordRLDecision(routePattern, rateLimitDecisionThrottle, rateLimitScopeIP)
			if state.RetryAfter > 0 {
				middleware_metrics.RecordRLRetryAfter(routePattern, state.RetryAfter)
				c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			}
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, rateLimitDecisionThrottle, state.Reason, state.RetryAfter, 0, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		// Kiểm tra trạng thái Block cục bộ:
		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionBlock {
			middleware_metrics.RecordRLCheck(routePattern, rateLimitScopeIP, rateLimitResultBlocked)
			middleware_metrics.RecordRLDecision(routePattern, rateLimitDecisionBlock, rateLimitScopeIP)
			c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			middleware_metrics.RecordRLRetryAfter(routePattern, state.RetryAfter)
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, rateLimitDecisionBlock, state.Reason, state.RetryAfter, state.RetryAfter, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		// Kiểm tra trạng thái Isolation cục bộ:
		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionIsolation {
			middleware_metrics.RecordRLCheck(routePattern, rateLimitScopeIP, rateLimitResultBlocked)
			middleware_metrics.RecordRLDecision(routePattern, rateLimitDecisionIsolation, rateLimitScopeIP)
			c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			middleware_metrics.RecordRLRetryAfter(routePattern, state.RetryAfter)
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, rateLimitDecisionIsolation, state.Reason, state.RetryAfter, state.RetryAfter, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// 🚀 BƯỚC B6: THỰC THI KIỂM TRA QUOTA BUCKET TRÊN REDIS (SLOW-PATH)
		// --------------------------------------------------------------------
		evalStart := time.Now()

		res, err := limiter.Allow(
			c.Request.Context(),
			key,
			ratelimit.Rate{
				Capacity: policyCfg.preAuthCapacity,
				Refill:   policyCfg.preAuthRefill,
				Period:   policyCfg.preAuthPeriod,
			},
			1,
		)

		// Đẩy các header chuẩn Rate Limit về cho Client:
		for k, v := range ratelimit.RateLimitHeaders(res) {
			c.Writer.Header().Set(k, v)
		}
		middleware_metrics.RecordRLEvalDuration(rateLimitScopeIP, time.Since(evalStart))

		// --------------------------------------------------------------------
		// 🚀 BƯỚC B7: ĐÁNH GIÁ KẾT QUẢ & CẬP NHẬT TRẠNG THÁI CHẶN (ACTION MAP)
		// --------------------------------------------------------------------

		// Trường hợp lỗi kết nối Redis (err != nil) -> Áp dụng chính sách Fail-Closed:
		// Để bảo vệ Database trung tâm khỏi bị DDoS sập nguồn khi bộ lọc chặn (Redis) bị hỏng,
		// chúng ta từ chối request và trả về lỗi 503 (Service Unavailable).
		if err != nil && !res.Allowed {
			middleware_metrics.RecordRLCheck(routePattern, rateLimitScopeIP, rateLimitResultError)
			middleware_metrics.RecordRLError(routePattern, "backend_unavailable")
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, "error", "backend_unavailable", 0, 0, key)
			apires.RespondServiceUnavailable(c, "rate limit temporarily unavailable")
			c.Abort()
			return
		}

		// Trường hợp cạn kiệt Token (Vượt quota giới hạn):
		if !res.Allowed {
			middleware_metrics.RecordRLCheck(routePattern, rateLimitScopeIP, rateLimitResultBlocked)
			middleware_metrics.RecordRLDecision(routePattern, rateLimitDecisionThrottle, rateLimitScopeIP)
			retryAfter := retryAfterFromRate(res)

			// Ghi nhận vi phạm vào local engine để block nhanh các request tiếp theo ở bộ nhớ RAM:
			rateLimitEngine.RecordThrottle(key)
			if retryAfter > 0 {
				middleware_metrics.RecordRLRetryAfter(routePattern, retryAfter)
			}
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, rateLimitDecisionThrottle, "capacity_exceeded", retryAfter, 0, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		// Request hợp lệ và nằm trong định mức cho phép:
		middleware_metrics.RecordRLCheck(routePattern, rateLimitScopeIP, rateLimitResultAllow)
		middleware_metrics.RecordRLDecision(routePattern, rateLimitResultAllow, rateLimitScopeIP)

		c.Next()
	}
}

// ============================================================================
// 🛡️ MIDDLEWARE 2: RATE LIMIT POST-AUTH (IDENTITY-AWARE ABUSE LIMITER)
// ============================================================================

// RateLimitPostAuth thực hiện giới hạn tần suất dựa trên thông tin định danh chi tiết.
// Chỉ áp dụng sau các middleware xác thực (Access, AdminAPIKeyAuth).
// Giúp tránh việc block nhầm các máy khách hợp lệ nằm sau cùng một NAT IP bằng cách ưu tiên key:
// 1) IP + Device ID
// 2) IP + User ID
// 3) IP (Fallback cuối cùng)
func RateLimitPostAuth(limiter *ratelimit.Bucket, path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// --------------------------------------------------------------------
		// 🚀 BƯỚC B1: KIỂM TRA BYPASS ROUTE PATTERN (SHORT-CIRCUIT)
		// --------------------------------------------------------------------
		if shouldBypassRateLimit(path) {
			middleware_metrics.RecordRLCheck(path, rateLimitScopeIPTracking, rateLimitResultBypass)
			c.Next()
			return
		}

		policyCfg := currentRateLimitPolicy()

		// --------------------------------------------------------------------
		// 🚀 BƯỚC B2: TRA CỨU NHANH CẤU HÌNH ĐƯỜNG DẪN (RAM MAP O(1))
		// --------------------------------------------------------------------
		rule, found := policyCfg.postAuthPathRules[path]
		if !found {
			// SRE không định nghĩa giới hạn cho đường dẫn này -> Cho qua (Bypass)
			middleware_metrics.RecordRLCheck(path, rateLimitScopeIPTracking, rateLimitResultAllow)
			c.Next()
			return
		}

		if limiter == nil {
			// Backend Redis chưa sẵn sàng -> Cho qua (Fail-Open) để tránh block nhầm
			middleware_metrics.RecordRLCheck(path, rateLimitScopeIPTracking, rateLimitResultAllow)
			c.Next()
			return
		}

		// --------------------------------------------------------------------
		// 🚀 BƯỚC B3: LẤY THÔNG TIN ĐỊNH DANH ĐỂ BUILD IDENTITY KEY
		// --------------------------------------------------------------------
		clientIP := clientIdentity(c)
		runtimeDeviceID := strings.TrimSpace(GetRuntimeAccessKey(c)) // Lấy Device Access Key
		userID := strings.TrimSpace(GetUserID(c))                    // Lấy User ID từ token claims

		// Xây dựng Key theo thứ tự ưu tiên giảm dần để tránh NAT Blocking:
		ruleScope := rateLimitScopeIPTracking
		key := ratelimit.KeyIPDevice(path, clientIP, runtimeDeviceID) // 1. IP + Device
		if key == "" {
			ruleScope = rateLimitScopeIPUser
			key = ratelimit.KeyIPUser(path, clientIP, userID) // 2. IP + User (Nếu thiếu Device)
		}
		if key == "" {
			ruleScope = rateLimitScopeIP
			key = ratelimit.KeyIP(path, clientIP) // 3. Fallback IP thô (Nếu chưa định danh)
		}

		// --------------------------------------------------------------------
		// 🚀 BƯỚC B4: KIỂM TRA ĐIỀU KIỆN KEY RỖNG
		// --------------------------------------------------------------------
		if key == "" {
			middleware_metrics.RecordRLCheck(path, ruleScope, rateLimitResultAllow)
			c.Next()
			return
		}

		// --------------------------------------------------------------------
		// 🚀 BƯỚC B5 & B6: KIỂM TRA TRẠNG THÁI TRONG LOCAL DENY CACHE
		// --------------------------------------------------------------------
		// Kiểm tra trạng thái Throttle cục bộ:
		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionThrottle {
			middleware_metrics.RecordRLCheck(path, ruleScope, rateLimitResultBlocked)
			middleware_metrics.RecordRLDecision(path, rateLimitDecisionThrottle, ruleScope)
			if state.RetryAfter > 0 {
				middleware_metrics.RecordRLRetryAfter(path, state.RetryAfter)
				c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			}
			emitRateLimitSecurityEvent(c, path, ruleScope, rateLimitDecisionThrottle, state.Reason, state.RetryAfter, 0, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		// Kiểm tra trạng thái Block cục bộ:
		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionBlock {
			middleware_metrics.RecordRLCheck(path, ruleScope, rateLimitResultBlocked)
			middleware_metrics.RecordRLDecision(path, rateLimitDecisionBlock, ruleScope)
			c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			middleware_metrics.RecordRLRetryAfter(path, state.RetryAfter)
			emitRateLimitSecurityEvent(c, path, ruleScope, rateLimitDecisionBlock, state.Reason, state.RetryAfter, state.RetryAfter, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		// Kiểm tra trạng thái Isolation cục bộ:
		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionIsolation {
			middleware_metrics.RecordRLCheck(path, ruleScope, rateLimitResultBlocked)
			middleware_metrics.RecordRLDecision(path, rateLimitDecisionIsolation, ruleScope)
			c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			middleware_metrics.RecordRLRetryAfter(path, state.RetryAfter)
			emitRateLimitSecurityEvent(c, path, ruleScope, rateLimitDecisionIsolation, state.Reason, state.RetryAfter, state.RetryAfter, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// 🚀 BƯỚC B7: ĐÁNH GIÁ QUOTA TRÊN REDIS TOKEN BUCKET (SLOW-PATH)
		// --------------------------------------------------------------------
		evalStart := time.Now()
		res, err := limiter.Allow(
			c.Request.Context(),
			key,
			ratelimit.Rate{
				Capacity: rule.Capacity,
				Refill:   rule.Refill,
				Period:   time.Duration(rule.PeriodSeconds) * time.Second,
			},
			1,
		)
		for k, v := range ratelimit.RateLimitHeaders(res) {
			c.Writer.Header().Set(k, v)
		}
		middleware_metrics.RecordRLEvalDuration(ruleScope, time.Since(evalStart))

		// --------------------------------------------------------------------
		// 🚀 BƯỚC B8: PHÂN LOẠI KẾT QUẢ & CẬP NHẬT TRẠNG THÁI CHẶN (ACTION MAP)
		// --------------------------------------------------------------------

		// Lỗi kết nối Redis -> Áp dụng Fail-Closed bảo vệ DB:
		if err != nil && !res.Allowed {
			middleware_metrics.RecordRLCheck(path, ruleScope, rateLimitResultError)
			middleware_metrics.RecordRLError(path, "backend_unavailable")
			emitRateLimitSecurityEvent(c, path, ruleScope, "error", "backend_unavailable", 0, 0, key)
			apires.RespondServiceUnavailable(c, "rate limit temporarily unavailable")
			c.Abort()
			return
		}

		// Quá quota -> Chặn và ghi nhận vi phạm vào local deny cache:
		if !res.Allowed {
			middleware_metrics.RecordRLCheck(path, ruleScope, rateLimitResultBlocked)
			middleware_metrics.RecordRLDecision(path, rateLimitDecisionThrottle, ruleScope)
			retryAfter := retryAfterFromRate(res)

			// Đánh dấu vi phạm để các request sau đó bị reject ngay trên RAM:
			rateLimitEngine.RecordThrottle(key)
			if retryAfter > 0 {
				middleware_metrics.RecordRLRetryAfter(path, retryAfter)
			}
			emitRateLimitSecurityEvent(c, path, ruleScope, rateLimitDecisionThrottle, "abuse_exceeded", retryAfter, 0, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		// Cho phép đi qua:
		middleware_metrics.RecordRLCheck(path, ruleScope, rateLimitResultAllow)
		middleware_metrics.RecordRLDecision(path, rateLimitResultAllow, ruleScope)
		c.Next()
	}
}

// ============================================================================
// 🛠️ CÁC HÀM TIỆN ÍCH & TRỢ GIÚP NỘI BỘ (HELPER FUNCTIONS)
// ============================================================================

// formatRetryAfterSeconds chuẩn hóa thời gian chờ (Duration) về dạng chuỗi giây nguyên dương.
// Thích hợp để ghi vào Header `Retry-After`.
func formatRetryAfterSeconds(retryAfter time.Duration) string {
	if retryAfter <= 0 {
		return "1"
	}
	seconds := int(retryAfter.Seconds())
	if float64(seconds) < retryAfter.Seconds() {
		seconds++ // Làm tròn lên để đảm bảo an toàn thời gian chờ
	}
	if seconds <= 0 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

// shouldBypassRateLimit xác định nhanh xem routePattern hiện tại có được miễn kiểm tra rate limit hay không.
func shouldBypassRateLimit(routePattern string) bool {
	_, ok := currentRateLimitPolicy().bypassRoutes[strings.TrimSpace(routePattern)]
	return ok
}

// routePatternOf lấy chuỗi route chuẩn hóa của Gin (Ví dụ: "/api/v1/users/:id").
// Nếu không lấy được FullPath, fallback về URL path thô của request.
func routePatternOf(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return "/"
	}
	if fullPath := strings.TrimSpace(c.FullPath()); fullPath != "" {
		return fullPath
	}
	path := strings.TrimSpace(c.Request.URL.Path)
	if path == "" {
		return "/"
	}
	return path
}

// retryAfterFromRate trả về thời gian chờ từ kết quả đánh giá của Redis Limiter.
// Nếu không xác định được, fallback về cấu hình mặc định (retryFallback) của hệ thống.
func retryAfterFromRate(res ratelimit.Result) time.Duration {
	if res.RetryAfter > 0 {
		return res.RetryAfter
	}
	return currentRateLimitPolicy().retryFallback
}

// emitRateLimitSecurityEvent thực hiện ghi log bảo mật (Security Event Log) cho các sự kiện rate limit.
// Sử dụng cơ chế Sampling để tránh bùng nổ dung lượng log khi bị tấn công DDoS hàng loạt.
func emitRateLimitSecurityEvent(c *gin.Context, routePattern, ruleScope, decision, escalationReason string, retryAfter, ttl time.Duration, subjectKey string) {
	// Kiểm tra điều kiện log sampling dựa trên loại quyết định (Throttle/Block/Isolation/Error):
	if !shouldEmitRateLimitSecurityEvent(c, decision, subjectKey) {
		return
	}

	fields := logger.Fields{
		"log_type":          "security_ratelimit",
		"request_id":        requestIDValue(c),
		"route_pattern":     routePattern,
		"rule_scope":        ruleScope,
		"decision":          decision,
		"escalation_reason": escalationReason,
		"retry_after_ms":    retryAfter.Milliseconds(),
		"ttl_ms":            ttl.Milliseconds(),
		"subject_key_hash":  hashSubjectKey(subjectKey), // Hash thông tin nhạy cảm trước khi ghi log
	}
	logger.L().WithFields(fields).Warn("security_ratelimit_decision")
}

// shouldEmitRateLimitSecurityEvent thực hiện phân phối mẫu log (Sampling) mang tính tất định.
// Sử dụng hàm băm SHA256 dựa trên: request_id + decision + subjectKey để tạo tính ổn định
// và nhất quán trên toàn bộ môi trường Cloud Native phân tán.
func shouldEmitRateLimitSecurityEvent(c *gin.Context, decision, subjectKey string) bool {
	samplePercent := samplingPercentByDecision(decision)
	if samplePercent >= 100 {
		return true
	}
	if samplePercent <= 0 {
		return false
	}
	seed := requestIDValue(c) + ":" + decision + ":" + subjectKey
	if strings.TrimSpace(seed) == "" {
		return false
	}
	hash := sha256.Sum256([]byte(seed))
	bucket := int(hash[0]) % 100 // Lấy byte đầu tiên để ánh xạ về dải 0-99
	return bucket < samplePercent
}

// samplingPercentByDecision lấy tỷ lệ mẫu log tương ứng với mức độ nghiêm trọng của sự kiện.
func samplingPercentByDecision(decision string) int {
	switch strings.TrimSpace(decision) {
	case "error":
		return currentRateLimitPolicy().errorSample
	case rateLimitDecisionThrottle:
		return currentRateLimitPolicy().throttleSample
	case rateLimitDecisionIsolation:
		return currentRateLimitPolicy().isolationSample
	case rateLimitDecisionBlock:
		return currentRateLimitPolicy().blockSample
	default:
		return currentRateLimitPolicy().errorSample
	}
}

// acquireGlobalInstantPermit kiểm tra và giữ 1 slot concurrency (inflight) cho request.
// Sử dụng vòng lặp Compare-And-Swap (CAS) an toàn đa luồng mà không gây block hệ thống.
func acquireGlobalInstantPermit() bool {
	cfg := currentRateLimitPolicy()
	if cfg.globalInstantMaxInflight <= 0 {
		return true
	}
	for {
		cur := rateLimitInflight.Load()
		if cur >= cfg.globalInstantMaxInflight {
			// Ghi nhận sự kiện reject do quá tải đồng thời cục bộ:
			middleware_metrics.RecordRLLocalCache("global_inflight_reject", rateLimitScopeIP)
			return false
		}
		if rateLimitInflight.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// releaseGlobalInstantPermit giải phóng 1 slot concurrency (inflight) khi request hoàn tất xử lý.
func releaseGlobalInstantPermit() {
	for {
		cur := rateLimitInflight.Load()
		if cur <= 0 {
			return
		}
		if rateLimitInflight.CompareAndSwap(cur, cur-1) {
			return
		}
	}
}

// requestIDValue lấy mã Request ID duy nhất để hỗ trợ truy vết (Forensics) từ Gin context.
func requestIDValue(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, _ := c.Get(logger.KeyRequestID)
	requestID, _ := v.(string)
	return strings.TrimSpace(requestID)
}

// hashSubjectKey băm subject key bằng SHA256 nhằm bảo mật thông tin IP/User/Device thô trong hệ thống ghi log tập trung.
func hashSubjectKey(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:])
}

// clientIdentity trích xuất IP của máy khách dựa trên các header ủy nhiệm (Proxy) từ Gin.
func clientIdentity(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}

	if ip := strings.TrimSpace(c.ClientIP()); ip != "" {
		return ip
	}

	addr := strings.TrimSpace(c.Request.RemoteAddr)
	if addr == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return strings.TrimSpace(host)
	}

	return addr
}
