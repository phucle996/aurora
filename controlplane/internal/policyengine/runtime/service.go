// ============================================================================
// 📂 FILE: runtime/service.go - Dịch Vụ Điều Phối Chính Sách Động (Engine Service)
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Đóng vai trò là "Container điều phối chính" (Generic Orchestration Container).
//   - Chịu trách nhiệm quản lý vòng đời của Snapshot chính sách, thực hiện reload,
//     phát hiện thay đổi (change detection), hoán đổi nguyên tử (atomic swap),
//     và truyền bá sự thay đổi thông qua Pub/Sub Redis.
//   - Hoàn toàn tách biệt khỏi các quy tắc nghiệp vụ/xác thực của từng loại chính sách cụ thể.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Dữ liệu cấu hình thô được nạp từ `PolicySourceAdapter` (local file `policy.yaml`).
//   - Trạng thái snapshot hoạt động trong bộ nhớ được lưu giữ trong trường `cur`.
//
// 🔒 RANH GIỚI BẢO MẬT/NGHIỆP VỤ (BOUNDARY):
//   - Triển khai nguyên tắc **Fail-Safe**: Bất kỳ lỗi cú pháp hoặc xác thực cấu hình nào
//     ở tệp YAML nguồn sẽ bị chặn đứng lập tức. Hệ thống sẽ giữ nguyên cấu hình tốt gần nhất (LKG).
//   - Đồng bộ luồng bằng `sync.RWMutex` bảo vệ việc đọc/ghi snapshot an toàn trong môi trường đa luồng.
//
// 🔄 CALLSITE FLOW:
//   - Được khởi chạy bởi `policyengine.New` qua hàm `Start(ctx)`.
//   - Middleware hoặc observability engine gọi `Current()` để lấy cấu hình hiện hành.
//   - `runReloadLoop` định kỳ quét sự thay đổi của tệp cấu hình 3 giây một lần.
//
// ============================================================================

package policyruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"controlplane/internal/config"
	policyErrorx "controlplane/internal/policyengine/errorx"
	"controlplane/internal/policyengine/policies/admincidr"
	"controlplane/internal/policyengine/policies/otel"
	"controlplane/internal/policyengine/policies/prometheus"
	"controlplane/internal/policyengine/policies/ratelimit"
	policytypes "controlplane/internal/policyengine/runtime/types"
	"controlplane/pkg/logger"

	"gopkg.in/yaml.v3"
)

const (
	defaultPolicyPollPeriod   = 3 * time.Second
	defaultPolicyVersion      = "v1"
	opPolicyReloadSuccess     = "policyengine.reload.success"
	opPolicyReloadFailed      = "policyengine.reload.failed"
	opPolicySourceDegraded    = "policyengine.source.degraded"
	opPolicyPropagationFailed = "policyengine.propagation.failed"
	defaultReloadCooldown     = 5 * time.Second
)

// PoliciesFile đại diện cho cấu trúc tệp YAML thô để unmarshal.
type PoliciesFile struct {
	Version  string              `yaml:"version"`
	Policies PoliciesRuntimeRoot `yaml:"policies"`
}

// PoliciesRuntimeRoot gom các nhánh cấu hình chính sách thô dưới dạng modular.
type PoliciesRuntimeRoot struct {
	AdminCIDR  admincidr.AdminCIDRPolicy   `yaml:"admin_cidr"`
	RateLimit  ratelimit.RateLimitPolicy   `yaml:"rate_limit"`
	OTel       otel.OTelPolicy             `yaml:"otel"`
	Prometheus prometheus.PrometheusPolicy `yaml:"prometheus"`
}

// EngineService là cấu trúc dịch vụ cốt lõi quản lý việc reload chính sách.
type EngineService struct {
	cfg *config.Config
	mu  sync.RWMutex
	cur *policytypes.PolicySet

	sourceAdapter   PolicySourceAdapter
	notifier        PolicyPropagationNotifier
	subscriber      PolicyEventSubscriber
	lastChecksum    string
	lastMetaKey     string
	lastReloadAt    time.Time
	otelHooks       []func(*otel.CompiledPolicy)
	prometheusHooks []func(*prometheus.CompiledPolicy)
	rateLimitHooks  []func(*ratelimit.CompiledPolicy)
}

// NewEngineService khởi tạo EngineService và liên kết các thành phần hạ tầng (source adapter, pub/sub notifier).
//
// # Tham số:
//   - `cfg`: Cấu hình hệ thống tĩnh toàn cục.
//   - `source`: Adapter để đọc tệp chính sách.
//   - `notifier`: Bộ truyền tin báo khi chính sách thay đổi.
//   - `subscriber`: Bộ đăng ký nhận tin báo thay đổi chính sách từ các instance khác.
//
// # Trả về:
//   - Con trỏ `EngineService` được cấu hình đầy đủ.
func NewEngineService(
	cfg *config.Config,
	source PolicySourceAdapter,
	notifier PolicyPropagationNotifier,
	subscriber PolicyEventSubscriber,
) *EngineService {
	return &EngineService{
		cfg:           cfg,
		sourceAdapter: source,
		notifier:      notifier,
		subscriber:    subscriber,
	}
}

// Start kích hoạt các worker chạy ngầm: vòng lặp poll tệp cấu hình và vòng lặp tiêu thụ thông báo Pub/Sub.
//
// # Tham số:
//   - `ctx`: Context quản lý vòng đời chạy nền của các worker.
func (s *EngineService) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go s.runReloadLoop(ctx)
	go s.runPropagationConsumeLoop(ctx)
}

// Current lấy snapshot chính sách đang hoạt động an toàn luồng.
// Trả về một bản copy nông của `PolicySet` để đảm bảo caller không thay đổi dữ liệu nội bộ của Engine.
//
// # Tham số:
//   - `ctx`: Context.
//
// # Trả về:
//   - Con trỏ `PolicySet`: Snapshot cấu hình đang hoạt động.
//   - `error`: Lỗi nếu hệ thống chưa tải thành công bất kỳ snapshot nào ban đầu.
func (s *EngineService) Current(ctx context.Context) (*policytypes.PolicySet, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cur == nil {
		return nil, fmt.Errorf("policy engine: no active policy set")
	}
	copyValue := *s.cur
	return &copyValue, nil
}

// RegisterOTelHook đăng ký một hàm callback (hook) nhận sự thay đổi cấu hình OpenTelemetry.
// Hook này chỉ được trigger khi cấu hình OTel thực sự có thay đổi so với snapshot trước đó.
//
// # Tham số:
//   - `hook`: Hàm callback chạy ngầm nhận vào cấu hình OTel đã được compile.
func (s *EngineService) RegisterOTelHook(hook func(*otel.CompiledPolicy)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.otelHooks = append(s.otelHooks, hook)
}

// RegisterPrometheusHook đăng ký một hàm callback (hook) nhận sự thay đổi cấu hình Prometheus.
// Hook này chỉ được trigger khi cấu hình Prometheus thực sự có thay đổi so với snapshot trước đó.
//
// # Tham số:
//   - `hook`: Hàm callback chạy ngầm nhận vào cấu hình Prometheus đã được compile.
func (s *EngineService) RegisterPrometheusHook(hook func(*prometheus.CompiledPolicy)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prometheusHooks = append(s.prometheusHooks, hook)
}

// RegisterRateLimitHook đăng ký một hàm callback (hook) nhận sự thay đổi cấu hình Rate Limit.
// Hook này chỉ được trigger khi cấu hình Rate Limit thực sự có thay đổi so với snapshot trước đó.
//
// # Tham số:
//   - `hook`: Hàm callback chạy ngầm nhận vào cấu hình Rate Limit đã được compile.
func (s *EngineService) RegisterRateLimitHook(hook func(*ratelimit.CompiledPolicy)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rateLimitHooks = append(s.rateLimitHooks, hook)
}

// Reload nạp tệp YAML thô, validate tính hợp lệ của toàn bộ policies, so sánh checksum,
// cooldown và hoán đổi nguyên tử snapshot nếu có thay đổi thực tế.
//
// # Tham số:
//   - `ctx`: Context xử lý.
//
// # Trả về:
//   - Con trỏ `PolicySet`: Snapshot cấu hình mới nhất vừa được áp dụng (hoặc snapshot cũ nếu không thay đổi).
//   - `error`: Lỗi nếu quá trình parse hoặc validate thất bại.
func (s *EngineService) Reload(ctx context.Context) (*policytypes.PolicySet, error) {
	if s.sourceAdapter != nil {
		// Tối ưu hóa: Kiểm tra metadata mtime/size trước để tránh đọc đĩa + parse YAML dư thừa.
		meta, err := s.sourceAdapter.ReadMeta(ctx)
		if err == nil {
			metaKey := strings.TrimSpace(meta.Path) + ":" + strings.TrimSpace(meta.Version) + ":" + fmt.Sprintf("%d", meta.Size)
			s.mu.RLock()
			unchanged := s.lastMetaKey != "" && s.lastMetaKey == metaKey
			current := s.cur
			s.mu.RUnlock()
			if unchanged {
				if current == nil {
					return nil, fmt.Errorf("policy engine: no active policy set")
				}
				copyValue := *current
				return &copyValue, nil
			}
		}
	}

	next, metaKey, err := s.loadPolicySnapshotFromSource(ctx)
	if err != nil {
		logger.SysWarnFields(opPolicyReloadFailed, "policy reload failed, keep last-known-good", err, logger.Fields{})
		return nil, err
	}
	s.mu.RLock()
	sameChecksum := s.lastChecksum != "" && s.lastChecksum == next.ChecksumSHA
	sameMeta := s.lastMetaKey != "" && s.lastMetaKey == metaKey
	s.mu.RUnlock()
	if sameChecksum || sameMeta {
		return next, nil
	}

	// Cơ chế Cooldown 5 giây để tránh hiện tượng dồn dập tải lại cấu hình (reload storm).
	now := time.Now().UTC()
	s.mu.RLock()
	inCooldown := !s.lastReloadAt.IsZero() && now.Sub(s.lastReloadAt) < defaultReloadCooldown
	s.mu.RUnlock()
	if inCooldown {
		return next, nil
	}

	s.mu.Lock()
	old := s.cur
	s.cur = next
	s.lastChecksum = next.ChecksumSHA
	s.lastMetaKey = metaKey
	s.lastReloadAt = now
	s.mu.Unlock()

	// Chỉ trigger các OTel hooks nếu cấu hình OTel thực sự thay đổi hoặc trong lần nạp đầu tiên.
	if old == nil || !reflect.DeepEqual(old.Runtime.OTel, next.Runtime.OTel) {
		s.mu.RLock()
		hooks := s.otelHooks
		s.mu.RUnlock()
		for _, hook := range hooks {
			go hook(&next.Runtime.OTel)
		}
	}

	// Chỉ trigger các Prometheus hooks nếu cấu hình Prometheus thực sự thay đổi hoặc trong lần nạp đầu tiên.
	if old == nil || !reflect.DeepEqual(old.Runtime.Prometheus, next.Runtime.Prometheus) {
		s.mu.RLock()
		hooks := s.prometheusHooks
		s.mu.RUnlock()
		for _, hook := range hooks {
			go hook(&next.Runtime.Prometheus)
		}
	}

	// Chỉ trigger các Rate Limit hooks nếu cấu hình Rate Limit thực sự thay đổi hoặc trong lần nạp đầu tiên.
	if old == nil || !reflect.DeepEqual(old.Runtime.RateLimit, next.Runtime.RateLimit) {
		s.mu.RLock()
		hooks := s.rateLimitHooks
		s.mu.RUnlock()
		for _, hook := range hooks {
			go hook(&next.Runtime.RateLimit)
		}
	}

	logger.SysInfoFields(opPolicyReloadSuccess, "policy snapshot swapped", logger.Fields{"version": next.Version, "checksum": next.ChecksumSHA, "source_mode": "poll", "trigger": "poll"})

	// Phát sự kiện lên hệ thống Pub/Sub
	if err := s.notifier.PublishPolicyChanged(ctx, policytypes.PolicyChangedEvent{
		Version:          next.Version,
		Checksum:         next.ChecksumSHA,
		SourceType:       "yaml_file",
		EmittedAtUnixSec: now.Unix(),
	}); err != nil {
		logger.SysWarnFields(opPolicyPropagationFailed, "policy propagation publish failed", err, logger.Fields{"version": next.Version, "checksum": next.ChecksumSHA, "trigger": "poll"})
	}
	return next, nil
}

// runReloadLoop chạy ngầm định kỳ quét tệp cấu hình.
func (s *EngineService) runReloadLoop(ctx context.Context) {
	ticker := time.NewTicker(defaultPolicyPollPeriod)
	defer ticker.Stop()
	_, _ = s.Reload(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.Reload(ctx)
		}
	}
}

// runPropagationConsumeLoop đăng ký Redis Pub/Sub lắng nghe thay đổi chính sách từ các node khác.
func (s *EngineService) runPropagationConsumeLoop(ctx context.Context) {
	if s.subscriber == nil {
		return
	}
	backoff := 2 * time.Second
	const maxBackoff = 30 * time.Second

	subscribe := func() (<-chan policytypes.PolicyChangedEvent, bool) {
		events, err := s.subscriber.SubscribePolicyChanged(ctx)
		if err != nil {
			logger.SysWarnFields(opPolicySourceDegraded, "policy propagation subscribe failed, retrying", err, logger.Fields{"source_mode": "poll", "retry_in_sec": int(backoff.Seconds())})
			select {
			case <-ctx.Done():
				return nil, false
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff = backoff * 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			return nil, false
		}
		backoff = 2 * time.Second
		return events, true
	}

	events, ok := subscribe()
	for !ok {
		if ctx.Err() != nil {
			return
		}
		events, ok = subscribe()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				logger.SysWarnFields(opPolicySourceDegraded, "policy propagation channel closed, resubscribing", nil, logger.Fields{"source_mode": "poll"})
				events, ok = subscribe()
				for !ok {
					if ctx.Err() != nil {
						return
					}
					events, ok = subscribe()
				}
				continue
			}
			s.mu.RLock()
			currentChecksum := s.lastChecksum
			s.mu.RUnlock()
			if strings.TrimSpace(event.Checksum) == "" || event.Checksum == currentChecksum {
				continue
			}
			_, _ = s.Reload(ctx)
		}
	}
}

// loadPolicySnapshotFromSource nạp file thô từ storage và biên dịch sang snapshot hợp lệ.
func (s *EngineService) loadPolicySnapshotFromSource(ctx context.Context) (*policytypes.PolicySet, string, error) {
	if s.sourceAdapter == nil {
		return nil, "", policyErrorx.ErrPolicyInvalid
	}
	rawBytes, meta, err := s.sourceAdapter.ReadCurrent(ctx)
	if err != nil {
		return nil, "", err
	}
	path := strings.TrimSpace(strings.ToLower(meta.Path))
	if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
		return nil, "", policyErrorx.ErrPolicyInvalid
	}
	parsed, runtimePolicies, err := parseAndCompilePolicies(rawBytes)
	if err != nil {
		return nil, "", err
	}
	version := strings.TrimSpace(parsed.Version)
	checksum := sha256.Sum256(rawBytes)
	now := time.Now().UTC()
	next := &policytypes.PolicySet{
		Version:     version,
		UpdatedAt:   now,
		Source:      meta.Path,
		ChecksumSHA: hex.EncodeToString(checksum[:]),
		Policies:    map[string]interface{}{"admin_cidr": runtimePolicies.AdminCIDR, "otel": runtimePolicies.OTel, "prometheus": runtimePolicies.Prometheus},
		Runtime:     runtimePolicies,
	}
	metaKey := strings.TrimSpace(meta.Path) + ":" + strings.TrimSpace(meta.Version) + ":" + fmt.Sprintf("%d", meta.Size)
	return next, metaKey, nil
}

// parseAndCompilePolicies phân tích cú pháp YAML thô và gọi các trình biên dịch modular.
func parseAndCompilePolicies(rawBytes []byte) (PoliciesFile, policytypes.RuntimePolicies, error) {
	parsed := PoliciesFile{}
	if err := yaml.Unmarshal(rawBytes, &parsed); err != nil {
		return PoliciesFile{}, policytypes.RuntimePolicies{}, err
	}
	if strings.TrimSpace(parsed.Version) == "" || strings.TrimSpace(parsed.Version) != defaultPolicyVersion {
		return PoliciesFile{}, policytypes.RuntimePolicies{}, policyErrorx.ErrPolicyInvalid
	}

	runtimeVariables, err := compilePolicies(parsed)
	if err != nil {
		return PoliciesFile{}, policytypes.RuntimePolicies{}, err
	}
	return parsed, runtimeVariables, nil
}

// compilePolicies ủy quyền (delegating) biên dịch cấu hình sang từng module tương ứng.
// Hàm này hoàn toàn không giữ bất kỳ logic nghiệp vụ kiểm tra cụ thể nào!
func compilePolicies(parsed PoliciesFile) (policytypes.RuntimePolicies, error) {
	cidr, err := admincidr.Compile(parsed.Policies.AdminCIDR)
	if err != nil {
		return policytypes.RuntimePolicies{}, err
	}

	rl, err := ratelimit.Compile(parsed.Policies.RateLimit)
	if err != nil {
		return policytypes.RuntimePolicies{}, err
	}

	ot, err := otel.Compile(parsed.Policies.OTel)
	if err != nil {
		return policytypes.RuntimePolicies{}, err
	}

	prom, err := prometheus.Compile(parsed.Policies.Prometheus)
	if err != nil {
		return policytypes.RuntimePolicies{}, err
	}

	return policytypes.RuntimePolicies{
		AdminCIDR:  cidr,
		RateLimit:  rl,
		OTel:       ot,
		Prometheus: prom,
	}, nil
}
