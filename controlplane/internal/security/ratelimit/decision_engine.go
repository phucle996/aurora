package ratelimit

import (
	"strings"
	"sync"
	"time"
)

const (
	DecisionAllow     = "allow"
	DecisionThrottle  = "throttle"
	DecisionIsolation = "temporary_isolation"
	DecisionBlock     = "block"
)

type EngineDecision struct {
	Decision   string
	Reason     string
	RetryAfter time.Duration
}

type DecisionEngine struct {
	mu   sync.RWMutex
	deny map[string]time.Time
}

// NewDecisionEngine khởi tạo state escalation cục bộ cho quyết định ratelimit.
// State enforcement được giữ ở core ratelimit để middleware chỉ còn nhiệm vụ thực thi.
func NewDecisionEngine() *DecisionEngine { return &DecisionEngine{deny: map[string]time.Time{}} }

// CheckActiveState trả về mức enforcement đang active có độ ưu tiên cao nhất cho subject.
// Thứ tự ưu tiên cố định: block (TTL tối đa 15 phút) > temporary_isolation (TTL 60 giây) > throttle (TTL 2 giây) > allow.
func (engine *DecisionEngine) CheckActiveState(subjectKey string) EngineDecision {
	now := time.Now()
	// Block là mức nặng nhất, nếu còn hiệu lực thì chặn ngay.
	if remaining, ok := engine.remaining("blk:"+subjectKey, now); ok {
		return EngineDecision{Decision: DecisionBlock, Reason: "block_active", RetryAfter: remaining}
	}
	// Isolation nhẹ hơn block nhưng vẫn là trạng thái chặn tạm thời.
	if remaining, ok := engine.remaining("iso:"+subjectKey, now); ok {
		return EngineDecision{Decision: DecisionIsolation, Reason: "isolation_active", RetryAfter: remaining}
	}
	// Marker deny cơ bản thể hiện throttle gần đây vẫn còn thời gian chờ.
	if remaining, ok := engine.remaining(subjectKey, now); ok {
		return EngineDecision{Decision: DecisionThrottle, Reason: "local_deny_cache", RetryAfter: remaining}
	}
	// Không có state nào đang active, request đi theo flow limiter bình thường.
	return EngineDecision{Decision: DecisionAllow}
}

// RecordThrottle ghi nhận subject vừa bị throttle bằng state TTL ngắn.
// Chỉ số escalation hiện tại:
// - throttle marker TTL: 2 giây
// - escalation window: 10 phút
// - ngưỡng block: >= 3 lần throttle trong escalation window
// - isolation TTL: 60 giây
// - block TTL: 15 phút
func (engine *DecisionEngine) RecordThrottle(subjectKey string) {
	// Marker deny cơ bản: tạo retry delay ngắn cho burst lặp lại.
	engine.set(subjectKey, 2*time.Second)
	// Marker escalation: đếm số lần throttle tái diễn trong cửa sổ cố định.
	prefix := "esc:" + subjectKey + ":"
	engine.set(prefix+time.Now().Format("150405.000000000"), 10*time.Minute)
	// Nếu số lần tái diễn chạm ngưỡng thì nâng lên block.
	if engine.countPrefix(prefix) >= 3 {
		engine.set("blk:"+subjectKey, 15*time.Minute)
		return
	}
	// Nếu chưa tới ngưỡng block thì nâng lên temporary isolation TTL ngắn hơn.
	engine.set("iso:"+subjectKey, 60*time.Second)
}

// remaining kiểm tra key còn hiệu lực hay không và tự dọn key hết hạn khi đọc.
// Cách dọn lazy này giảm áp lực ghi ở hot-path và tránh phải quét toàn bộ map mỗi request.
func (engine *DecisionEngine) remaining(key string, now time.Time) (time.Duration, bool) {
	engine.mu.RLock()
	expiresAt, ok := engine.deny[key]
	engine.mu.RUnlock()
	if !ok {
		return 0, false
	}
	if now.After(expiresAt) {
		engine.mu.Lock()
		if current, exists := engine.deny[key]; exists && now.After(current) {
			delete(engine.deny, key)
		}
		engine.mu.Unlock()
		return 0, false
	}
	remaining := time.Until(expiresAt)
	return remaining, remaining > 0
}

func (engine *DecisionEngine) set(key string, ttl time.Duration) {
	if strings.TrimSpace(key) == "" {
		return
	}
	if ttl <= 0 {
		ttl = 2 * time.Second
	}
	if ttl > 15*time.Minute {
		ttl = 15 * time.Minute
	}

	expiresAt := time.Now().Add(ttl)

	engine.mu.Lock()
	defer engine.mu.Unlock()
	// Hard cap ngăn map nở vô hạn khi cardinality subject tăng đột biến (max 4096 keys).
	if len(engine.deny) >= 4096 {
		// Mỗi lần đầy chỉ quét tối đa 128 key để giới hạn lock hold-time.
		engine.evictExpiredLocked(time.Now(), 128)
		if len(engine.deny) >= 4096 {
			// Nếu vẫn đầy sau cleanup có giới hạn thì bỏ qua key mới để bảo vệ memory.
			return
		}
	}
	engine.deny[key] = expiresAt
}

// countPrefix đếm số marker tái phạm còn hiệu lực để quyết định escalation.
// Chỉ đếm marker chưa hết hạn để lịch sử cũ tự decay theo TTL (window hiện tại: 10 phút).
func (engine *DecisionEngine) countPrefix(prefix string) int {
	now := time.Now()
	count := 0
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	for key, expiresAt := range engine.deny {
		if strings.HasPrefix(key, prefix) && now.Before(expiresAt) {
			count++
		}
	}
	return count
}

// evictExpiredLocked dọn key hết hạn với số bước scan giới hạn.
// maxScan càng nhỏ thì lock hold-time càng thấp; hiện dùng 128 để cân bằng cleanup/cost.
func (engine *DecisionEngine) evictExpiredLocked(now time.Time, maxScan int) {
	scanned := 0
	for key, expiresAt := range engine.deny {
		scanned++
		if now.After(expiresAt) {
			delete(engine.deny, key)
		}
		if scanned >= maxScan {
			break
		}
	}
}
