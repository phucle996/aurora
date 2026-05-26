package middleware

import (
	"controlplane/internal/observability"
	policytypes "controlplane/internal/policyengine/runtime/types"
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

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	rateLimitScopeIP            = "ip"
	rateLimitScopeIPTracking    = "ip_tracking"
	rateLimitScopeIPUser        = "ip_user"
	rateLimitResultAllow        = "allow"
	rateLimitResultBlocked      = "blocked"
	rateLimitResultBypass       = "bypass"
	rateLimitResultError        = "error"
	rateLimitDecisionThrottle   = "throttle"
	rateLimitDecisionIsolation  = "temporary_isolation"
	rateLimitDecisionBlock      = "block"
)

var rateLimitEngine = ratelimit.NewDecisionEngine()

type rateLimitPolicyConfig struct {
	preAuthCapacity          int64
	preAuthRefill            int64
	preAuthPeriod            time.Duration
	postAuthCapacity         int64
	postAuthRefill           int64
	postAuthPeriod           time.Duration
	globalInstantMaxInflight int64
	globalInstantRetryAfter  time.Duration
	retryFallback            time.Duration
	throttleSample           int
	isolationSample          int
	blockSample              int
	errorSample              int
	bypassRoutes             map[string]struct{}
}

var rateLimitPolicyHolder atomic.Value
var rateLimitInflight atomic.Int64

func currentRateLimitPolicy() rateLimitPolicyConfig {
	v := rateLimitPolicyHolder.Load()
	if v == nil {
		panic("ratelimiter: runtime policy is not initialized")
	}
	return v.(rateLimitPolicyConfig)
}

// InitRateLimitPolicy nạp runtime policy cho rate-limit và fallback về default an toàn nếu thiếu key.
func InitRateLimitPolicy(policy policytypes.CompiledRateLimitPolicyGroup) {
	bypassRoutes := map[string]struct{}{}
	for _, route := range policy.Behavior.BypassRoutePatterns {
		bypassRoutes[route] = struct{}{}
	}
	cfg := rateLimitPolicyConfig{
		preAuthCapacity:          policy.PreAuth.IP.Capacity,
		preAuthRefill:            policy.PreAuth.IP.Refill,
		preAuthPeriod:            time.Duration(policy.PreAuth.IP.PeriodSeconds) * time.Second,
		postAuthCapacity:         policy.PostAuth.IPDevice.Capacity,
		postAuthRefill:           policy.PostAuth.IPDevice.Refill,
		postAuthPeriod:           time.Duration(policy.PostAuth.IPDevice.PeriodSeconds) * time.Second,
		globalInstantMaxInflight: policy.PreAuth.GlobalInstant.MaxInflight,
		globalInstantRetryAfter:  time.Duration(policy.PreAuth.GlobalInstant.RetryAfterSeconds) * time.Second,
		retryFallback:            time.Duration(policy.Behavior.RetryAfterFallbackSeconds) * time.Second,
		throttleSample:           policy.Observability.SamplingPercent.Throttle,
		isolationSample:          policy.Observability.SamplingPercent.TemporaryIsolation,
		blockSample:              policy.Observability.SamplingPercent.Block,
		errorSample:              policy.Observability.SamplingPercent.Error,
		bypassRoutes:             bypassRoutes,
	}
	rateLimitPolicyHolder.Store(cfg)
}

var (
	rateLimitCheckTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: "security",
		Name:      "ratelimit_check_total",
		Help:      "Total number of rate limit checks by route/rule/result.",
	}, []string{"route_pattern", "rule_scope", "result"})

	rateLimitDecisionTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: "security",
		Name:      "ratelimit_decision_total",
		Help:      "Total number of final rate limit decisions by route/scope.",
	}, []string{"route_pattern", "decision", "rule_scope"})

	rateLimitErrorTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: "security",
		Name:      "ratelimit_error_total",
		Help:      "Total number of rate limit evaluation errors by route/error type.",
	}, []string{"route_pattern", "error_type"})

	rateLimitEvalDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Subsystem: "security",
		Name:      "ratelimit_eval_duration_seconds",
		Help:      "Rate limit evaluator duration by rule scope.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"rule_scope"})

	rateLimitRetryAfter = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Subsystem: "security",
		Name:      "ratelimit_retry_after_seconds",
		Help:      "Retry-After seconds returned by rate limiter.",
		Buckets:   []float64{1, 2, 3, 5, 8, 13, 21, 34, 55, 89},
	}, []string{"route_pattern"})

	rateLimitLocalCacheTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: "security",
		Name:      "ratelimit_local_cache_total",
		Help:      "Local deny-cache events by action and rule scope.",
	}, []string{"action", "rule_scope"})
)

func init() {
	observability.RegisterModuleMetrics(RegisterRateLimitMetrics)
}

// RegisterRateLimitMetrics đăng ký toàn bộ metrics anti-probing do middleware phát ra.
// Cách dùng:
// - Không gọi trực tiếp ở route/handler.
// - Hàm này được observability bootstrap gọi qua RegisterModuleMetrics trong init().
// - Mỗi process chỉ cần register 1 lần; repeated register sẽ được bỏ qua bằng AlreadyRegisteredError.
// Chỉ số theo dõi chính: block rate, backend error rate, eval latency theo rule scope.
func RegisterRateLimitMetrics(registry *prometheus.Registry, namespace string) error {
	_ = namespace
	collectors := []prometheus.Collector{
		rateLimitCheckTotal,
		rateLimitDecisionTotal,
		rateLimitErrorTotal,
		rateLimitEvalDuration,
		rateLimitRetryAfter,
		rateLimitLocalCacheTotal,
	}
	for _, collector := range collectors {
		if err := registry.Register(collector); err != nil {
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				return err
			}
		}
	}

	return nil
}

// RateLimitPreAuth xử lý admission trước auth với key theo IP.
// Các case chính:
// 1) bypass endpoint health/metrics -> cho qua ngay,
// 2) state đang active (throttle/isolation/block) -> trả 429 + Retry-After,
// 3) backend limiter lỗi và request bị deny -> trả 503,
// 4) vượt ngưỡng bucket -> throttle và ghi dấu escalation.
// Cách dùng:
// - Gắn ở global middleware chain (app bootstrap), trước AccessLog và trước các auth/business middleware.
// - Không gắn lại ở per-route nếu đã dùng global preauth để tránh double enforcement.
// - capacity/refill/period là baseline admission cho toàn app hoặc nhóm route ở mức global.
// Chỉ số theo dõi đề xuất cho nhánh middleware này:
// - p95 eval_duration_seconds < 5ms (không tính RTT Redis),
// - tỉ lệ backend_unavailable trên tổng check < 0.1% trong trạng thái bình thường.
// Flow nội bộ theo từng bước:
// - B1: lấy routePattern và check bypass list.
// - B2: fail-open local cho config/limiter invalid (cho qua, có metric allow).
// - B3: build key từ IP; thiếu key thì cho qua.
// - B4: hỏi decision engine state active (throttle/isolation/block).
// - B5: nếu state active thì trả 429 ngay + Retry-After + security event log.
// - B6: nếu không active thì gọi Redis token-bucket evaluate.
// - B7: map kết quả evaluate sang allow/error/throttle và emit metric/log tương ứng.
func RateLimitPreAuth(limiter *ratelimit.Bucket, name string, capacity, refill int64, period time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		routePattern := routePatternOf(c)
		if shouldBypassRateLimit(routePattern) {
			rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIP, rateLimitResultBypass).Inc()
			c.Next()
			return
		}
		if !acquireGlobalInstantPermit() {
			retryAfter := currentRateLimitPolicy().globalInstantRetryAfter
			c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(retryAfter))
			rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIP, rateLimitResultBlocked).Inc()
			rateLimitDecisionTotal.WithLabelValues(routePattern, rateLimitDecisionThrottle, rateLimitScopeIP).Inc()
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, rateLimitDecisionThrottle, "global_instant_cap", retryAfter, 0, "global")
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}
		defer releaseGlobalInstantPermit()

		if limiter == nil || name == "" || capacity <= 0 || refill <= 0 || period <= 0 {
			rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIP, rateLimitResultAllow).Inc()
			c.Next()
			return
		}

		key := ratelimit.Key("", name, clientIdentity(c))
		if key == "" {
			rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIP, rateLimitResultAllow).Inc()
			c.Next()
			return
		}

		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionThrottle {
			rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIP, rateLimitResultBlocked).Inc()
			rateLimitDecisionTotal.WithLabelValues(routePattern, rateLimitDecisionThrottle, rateLimitScopeIP).Inc()
			if state.RetryAfter > 0 {
				rateLimitRetryAfter.WithLabelValues(routePattern).Observe(state.RetryAfter.Seconds())
				c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			}
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, rateLimitDecisionThrottle, state.Reason, state.RetryAfter, 0, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionBlock {
			rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIP, rateLimitResultBlocked).Inc()
			rateLimitDecisionTotal.WithLabelValues(routePattern, rateLimitDecisionBlock, rateLimitScopeIP).Inc()
			c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			rateLimitRetryAfter.WithLabelValues(routePattern).Observe(state.RetryAfter.Seconds())
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, rateLimitDecisionBlock, state.Reason, state.RetryAfter, state.RetryAfter, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionIsolation {
			rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIP, rateLimitResultBlocked).Inc()
			rateLimitDecisionTotal.WithLabelValues(routePattern, rateLimitDecisionIsolation, rateLimitScopeIP).Inc()
			c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			rateLimitRetryAfter.WithLabelValues(routePattern).Observe(state.RetryAfter.Seconds())
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, rateLimitDecisionIsolation, state.Reason, state.RetryAfter, state.RetryAfter, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		evalStart := time.Now()

		policyCfg := currentRateLimitPolicy()
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

		for k, v := range ratelimit.RateLimitHeaders(res) {
			c.Writer.Header().Set(k, v)
		}
		rateLimitEvalDuration.WithLabelValues(rateLimitScopeIP).Observe(time.Since(evalStart).Seconds())

		if err != nil && !res.Allowed {
			rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIP, rateLimitResultError).Inc()
			rateLimitErrorTotal.WithLabelValues(routePattern, "backend_unavailable").Inc()
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, "error", "backend_unavailable", 0, 0, key)
			apires.RespondServiceUnavailable(c, "rate limit temporarily unavailable")
			c.Abort()
			return
		}

		if !res.Allowed {
			rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIP, rateLimitResultBlocked).Inc()
			rateLimitDecisionTotal.WithLabelValues(routePattern, rateLimitDecisionThrottle, rateLimitScopeIP).Inc()
			retryAfter := retryAfterFromRate(res)
			rateLimitEngine.RecordThrottle(key)
			if retryAfter > 0 {
				rateLimitRetryAfter.WithLabelValues(routePattern).Observe(retryAfter.Seconds())
			}
			emitRateLimitSecurityEvent(c, routePattern, rateLimitScopeIP, rateLimitDecisionThrottle, "capacity_exceeded", retryAfter, 0, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIP, rateLimitResultAllow).Inc()
		rateLimitDecisionTotal.WithLabelValues(routePattern, rateLimitResultAllow, rateLimitScopeIP).Inc()

		c.Next()
	}
}

// RateLimitPostAuth applies identity-aware abuse rate limit.
//
// Rule priority:
// 1) ip + runtime_device_id
// 2) ip + user_id
//
// If both identity dimensions are missing, middleware skips enforcement.
// Mục tiêu là giảm block oan trong NAT bằng identity-aware key trước khi fallback IP-only.
// Cách dùng:
// - Gắn sau middleware auth guard (Access/AdminAPIKeyAuth) để context có user/device identity.
// - Dùng cho route nhạy cảm (auth/admin/me-device), không cần gắn cho health/public không cần identity.
// - Nếu thiếu cả device/user identity thì middleware cho qua để tránh fail cứng sai ngữ cảnh.
// Chỉ số theo dõi đề xuất:
// - tỉ lệ false-positive (đánh giá qua complaint/manual review) giảm theo rollout,
// - p95 eval_duration_seconds ở postauth không vượt preauth quá 20%.
// Flow nội bộ theo từng bước:
// - B1: lấy routePattern và check bypass list.
// - B2: fail-open local cho config/limiter invalid (cho qua, có metric allow).
// - B3: lấy identity context từ middleware trước (device/user) và build key theo ưu tiên.
// - B4: nếu không build được key thì cho qua để tránh deny sai ngữ cảnh.
// - B5: hỏi decision engine state active (throttle/isolation/block).
// - B6: nếu state active thì trả 429 ngay + Retry-After + security event log.
// - B7: nếu không active thì gọi Redis token-bucket evaluate.
// - B8: map kết quả evaluate sang allow/error/throttle và emit metric/log tương ứng.
func RateLimitPostAuth(limiter *ratelimit.Bucket, name string, capacity, refill int64, period time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		routePattern := routePatternOf(c)
		if shouldBypassRateLimit(routePattern) {
			rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIPTracking, rateLimitResultBypass).Inc()
			c.Next()
			return
		}

		if limiter == nil || name == "" || capacity <= 0 || refill <= 0 || period <= 0 {
			rateLimitCheckTotal.WithLabelValues(routePattern, rateLimitScopeIPTracking, rateLimitResultAllow).Inc()
			c.Next()
			return
		}

		clientIP := clientIdentity(c)
		runtimeDeviceID := strings.TrimSpace(GetRuntimeDeviceID(c))
		userID := strings.TrimSpace(GetUserID(c))

		ruleScope := rateLimitScopeIPTracking
		key := ratelimit.Key(name, rateLimitScopeIPTracking, strings.TrimSpace(clientIP)+":"+runtimeDeviceID)
		if key == "" {
			ruleScope = rateLimitScopeIPUser
			key = ratelimit.KeyIPUser(name, clientIP, userID)
		}

		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionThrottle {
			rateLimitCheckTotal.WithLabelValues(routePattern, ruleScope, rateLimitResultBlocked).Inc()
			rateLimitDecisionTotal.WithLabelValues(routePattern, rateLimitDecisionThrottle, ruleScope).Inc()
			if state.RetryAfter > 0 {
				rateLimitRetryAfter.WithLabelValues(routePattern).Observe(state.RetryAfter.Seconds())
				c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			}
			emitRateLimitSecurityEvent(c, routePattern, ruleScope, rateLimitDecisionThrottle, state.Reason, state.RetryAfter, 0, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionBlock {
			rateLimitCheckTotal.WithLabelValues(routePattern, ruleScope, rateLimitResultBlocked).Inc()
			rateLimitDecisionTotal.WithLabelValues(routePattern, rateLimitDecisionBlock, ruleScope).Inc()
			c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			rateLimitRetryAfter.WithLabelValues(routePattern).Observe(state.RetryAfter.Seconds())
			emitRateLimitSecurityEvent(c, routePattern, ruleScope, rateLimitDecisionBlock, state.Reason, state.RetryAfter, state.RetryAfter, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		if state := rateLimitEngine.CheckActiveState(key); state.Decision == ratelimit.DecisionIsolation {
			rateLimitCheckTotal.WithLabelValues(routePattern, ruleScope, rateLimitResultBlocked).Inc()
			rateLimitDecisionTotal.WithLabelValues(routePattern, rateLimitDecisionIsolation, ruleScope).Inc()
			c.Writer.Header().Set("Retry-After", formatRetryAfterSeconds(state.RetryAfter))
			rateLimitRetryAfter.WithLabelValues(routePattern).Observe(state.RetryAfter.Seconds())
			emitRateLimitSecurityEvent(c, routePattern, ruleScope, rateLimitDecisionIsolation, state.Reason, state.RetryAfter, state.RetryAfter, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}
		if key == "" {
			rateLimitCheckTotal.WithLabelValues(routePattern, ruleScope, rateLimitResultAllow).Inc()
			c.Next()
			return
		}

		evalStart := time.Now()
		policyCfg := currentRateLimitPolicy()
		res, err := limiter.Allow(
			c.Request.Context(),
			key,
			ratelimit.Rate{
				Capacity: policyCfg.postAuthCapacity,
				Refill:   policyCfg.postAuthRefill,
				Period:   policyCfg.postAuthPeriod,
			},
			1,
		)
		for k, v := range ratelimit.RateLimitHeaders(res) {
			c.Writer.Header().Set(k, v)
		}
		rateLimitEvalDuration.WithLabelValues(ruleScope).Observe(time.Since(evalStart).Seconds())

		if err != nil && !res.Allowed {
			rateLimitCheckTotal.WithLabelValues(routePattern, ruleScope, rateLimitResultError).Inc()
			rateLimitErrorTotal.WithLabelValues(routePattern, "backend_unavailable").Inc()
			emitRateLimitSecurityEvent(c, routePattern, ruleScope, "error", "backend_unavailable", 0, 0, key)
			apires.RespondServiceUnavailable(c, "rate limit temporarily unavailable")
			c.Abort()
			return
		}

		if !res.Allowed {
			rateLimitCheckTotal.WithLabelValues(routePattern, ruleScope, rateLimitResultBlocked).Inc()
			rateLimitDecisionTotal.WithLabelValues(routePattern, rateLimitDecisionThrottle, ruleScope).Inc()
			retryAfter := retryAfterFromRate(res)
			rateLimitEngine.RecordThrottle(key)
			if retryAfter > 0 {
				rateLimitRetryAfter.WithLabelValues(routePattern).Observe(retryAfter.Seconds())
			}
			emitRateLimitSecurityEvent(c, routePattern, ruleScope, rateLimitDecisionThrottle, "abuse_exceeded", retryAfter, 0, key)
			apires.RespondTooManyRequests(c, "too many requests")
			c.Abort()
			return
		}

		rateLimitCheckTotal.WithLabelValues(routePattern, ruleScope, rateLimitResultAllow).Inc()
		rateLimitDecisionTotal.WithLabelValues(routePattern, rateLimitResultAllow, ruleScope).Inc()
		c.Next()
	}
}

// formatRetryAfterSeconds chuẩn hóa Retry-After về số giây nguyên dương.
// Cách dùng:
// - Chỉ dùng để set HTTP header Retry-After cho nhánh deny.
// - duration <= 0 vẫn trả về tối thiểu 1 giây để client luôn có backoff hợp lệ.
func formatRetryAfterSeconds(retryAfter time.Duration) string {
	if retryAfter <= 0 {
		return "1"
	}
	seconds := int(retryAfter.Seconds())
	if float64(seconds) < retryAfter.Seconds() {
		seconds++
	}
	if seconds <= 0 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

// shouldBypassRateLimit xác định các endpoint luôn được ưu tiên sống.
// Các endpoint này không được để limiter chặn vì ảnh hưởng trực tiếp liveness/metrics scraping.
// Cách dùng:
// - Được gọi ở đầu PreAuth/PostAuth để short-circuit.
// - Nếu thêm endpoint mới kiểu health/ops, cần cập nhật tại đây để tránh false deny.
func shouldBypassRateLimit(routePattern string) bool {
	_, ok := currentRateLimitPolicy().bypassRoutes[strings.TrimSpace(routePattern)]
	return ok
}

// routePatternOf lấy route đã normalize (nếu có), fallback về URL path.
// Mục tiêu là giữ cardinality metrics/log ổn định để không bùng nổ chi phí quan sát.
// Cách dùng:
// - Dùng cho label route_pattern trong metrics/log của rate-limit.
// - Ưu tiên FullPath() để tránh mỗi URL param tạo 1 metric series riêng.
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

// retryAfterFromRate đảm bảo nhánh 429 luôn có backoff nhất quán.
// Nếu limiter không trả RetryAfter thì dùng mặc định 2 giây để tránh retry storm tức thời.
// Cách dùng:
// - Chỉ gọi sau khi quyết định deny vì quota exceeded.
func retryAfterFromRate(res ratelimit.Result) time.Duration {
	if res.RetryAfter > 0 {
		return res.RetryAfter
	}
	return currentRateLimitPolicy().retryFallback
}

// emitRateLimitSecurityEvent ghi log security riêng cho anti-probing.
// Case xử lý: chỉ ghi khi sampling cho phép; subject key luôn hash để tránh lộ dữ liệu nhạy cảm.
// Chỉ số theo dõi chi phí log:
// - decision=throttle dùng sampling thấp,
// - decision=block/error giữ sampling cao để đủ forensic.
// Cách dùng:
// - Gọi ở mọi nhánh deny/error trước khi trả response.
// - Không dùng cho success path để tránh phình chi phí log.
func emitRateLimitSecurityEvent(c *gin.Context, routePattern, ruleScope, decision, escalationReason string, retryAfter, ttl time.Duration, subjectKey string) {
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
		"subject_key_hash":  hashSubjectKey(subjectKey),
	}
	logger.L().WithFields(fields).Warn("security_ratelimit_decision")
}

// shouldEmitRateLimitSecurityEvent quyết định có ghi log hay không theo sampling tất định.
// Cách làm: hash(request_id + decision + subject) để phân phối ổn định, tránh lệch ngẫu nhiên.
// Cách dùng:
// - Chỉ dùng nội bộ bởi emitRateLimitSecurityEvent.
// - Không dùng làm quyết định security, chỉ để kiểm soát chi phí observability.
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
	bucket := int(hash[0]) % 100
	return bucket < samplePercent
}

// samplingPercentByDecision định nghĩa sampling mặc định theo mức độ nghiêm trọng.
// Chỉ số hiện tại:
// - throttle: 10%
// - temporary_isolation: 50%
// - block: 100%
// - error: 100%
// Chỉ số theo dõi: giảm log amplification nhưng vẫn đủ dữ liệu điều tra sự cố.
// Cách dùng:
// - Mapping này là default policy tại code-level.
// - Khi có config runtime policy sau này, mapping này sẽ là fallback mặc định.
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

func acquireGlobalInstantPermit() bool {
	cfg := currentRateLimitPolicy()
	if cfg.globalInstantMaxInflight <= 0 {
		return true
	}
	for {
		cur := rateLimitInflight.Load()
		if cur >= cfg.globalInstantMaxInflight {
			rateLimitLocalCacheTotal.WithLabelValues("global_inflight_reject", rateLimitScopeIP).Inc()
			return false
		}
		if rateLimitInflight.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

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

// requestIDValue lấy request_id đã inject từ middleware RequestID để correlate log/trace.
// Cách dùng: chỉ phục vụ log correlation, không dùng cho identity/security decision.
func requestIDValue(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, _ := c.Get(logger.KeyRequestID)
	requestID, _ := v.(string)
	return strings.TrimSpace(requestID)
}

// hashSubjectKey băm subject key trước khi ghi log để tránh lộ dữ liệu thô.
// Đồng thời vẫn giữ khả năng group sự kiện theo cùng subject trong quá trình điều tra.
// Cách dùng: luôn gọi trước khi ghi subject vào log field.
func hashSubjectKey(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:])
}

// clientIdentity lấy IP client theo thứ tự ưu tiên:
// 1) gin ClientIP
// 2) parse RemoteAddr
// 3) fallback raw addr
// Mục tiêu là luôn có identity tối thiểu cho preauth limiter.
// Cách dùng:
// - Dùng ở preauth để build subject key.
// - Không coi đây là identity mạnh; postauth sẽ ưu tiên device/user khi có context.
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
