package policyruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"controlplane/internal/config"
	policyConfigYAML "controlplane/internal/policyengine/runtime/configyaml"
	policyEntity "controlplane/internal/policyengine/runtime/types"
	policyErrorx "controlplane/internal/policyengine/errorx"
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

type yamlPolicyFile struct {
	Version  string                 `yaml:"version"`
	Policies map[string]interface{} `yaml:"policies"`
}

type EngineService struct {
	cfg *config.Config
	mu  sync.RWMutex
	cur *policyEntity.PolicySet

	sourceAdapter PolicySourceAdapter
	notifier      PolicyPropagationNotifier
	subscriber    PolicyEventSubscriber
	lastChecksum  string
	lastMetaKey   string
	lastReloadAt  time.Time
}

// NewEngineService only provisions dependencies for runtime reload engine.
// Worker lifecycle is owned by module/bootstrap via Start(ctx).
//
// CONTRACT:
// - Service này chỉ quản lý runtime hot-reload mechanics, không sở hữu business policy semantics.
// - SoT của policy là YAML source adapter; service không đọc DB.
// - Các dependency fail-fast được validate ở module boundary trước khi khởi tạo.
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

// Start runs runtime workers for reload and cross-instance propagation.
// Constructor only provisions dependencies; module/bootstrap owns goroutine lifecycle.
//
// BOUNDARY:
// - Start chỉ khởi chạy worker loop; không tự ý re-wire dependency.
// - Stop lifecycle do module quản lý bằng context cancel.
func (s *EngineService) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go s.runReloadLoop(ctx)
	go s.runPropagationConsumeLoop(ctx)
}

// Current trả snapshot active dạng copy để caller không mutate state nội bộ.
// Nếu chưa có snapshot active thì fail-fast để tránh runtime dùng policy mơ hồ.
func (s *EngineService) Current(ctx context.Context) (*policyEntity.PolicySet, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cur == nil {
		return nil, fmt.Errorf("policy engine: no active policy set")
	}
	copyValue := *s.cur
	return &copyValue, nil
}

// Reload thực thi pipeline load/validate/swap theo invariant runtime.
// Invariant cốt lõi: invalid source hoặc error không bao giờ được phép ghi đè active snapshot.
//
// Flow nội bộ theo từng bước:
// - B1: metadata-first gate (mtime/size) để skip read+parse khi source không đổi.
// - B2: load full source + validate YAML contract (`version=v1`, `policies` non-empty).
// - B3: dedupe bằng checksum/meta key để tránh swap lặp.
// - B4: cooldown 5s để chống reload storm khi trigger dồn dập.
// - B5: atomic swap + cập nhật marker và ghi log success.
func (s *EngineService) Reload(ctx context.Context) (*policyEntity.PolicySet, error) {
	if s.sourceAdapter != nil {
		// Case: source chưa đổi theo metadata.
		// Action: trả ngay snapshot hiện tại, không read file/parse YAML để tránh nghẽn I/O.
		// Mechanism: so sánh metaKey(path+version+size) với marker lần reload gần nhất.
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

	// Case: trigger burst trong cửa sổ ngắn sau reload thành công.
	// Action: skip swap trong 5s để giữ ổn định CPU và giảm noise.
	// Mechanism: timestamp gate in-memory (`lastReloadAt`), reset khi process restart.
	now := time.Now().UTC()
	s.mu.RLock()
	inCooldown := !s.lastReloadAt.IsZero() && now.Sub(s.lastReloadAt) < defaultReloadCooldown
	s.mu.RUnlock()
	if inCooldown {
		return next, nil
	}
	s.mu.Lock()
	s.cur = next
	s.lastChecksum = next.ChecksumSHA
	s.lastMetaKey = metaKey
	s.lastReloadAt = now
	s.mu.Unlock()
	logger.SysInfoFields(opPolicyReloadSuccess, "policy snapshot swapped", logger.Fields{"version": next.Version, "checksum": next.ChecksumSHA, "source_mode": "poll", "trigger": "poll"})
	if err := s.notifier.PublishPolicyChanged(ctx, policyEntity.PolicyChangedEvent{
		Version:          next.Version,
		Checksum:         next.ChecksumSHA,
		SourceType:       "yaml_file",
		EmittedAtUnixSec: now.Unix(),
	}); err != nil {
		logger.SysWarnFields(opPolicyPropagationFailed, "policy propagation publish failed", err, logger.Fields{"version": next.Version, "checksum": next.ChecksumSHA, "trigger": "poll"})
	}
	return next, nil
}

// runReloadLoop là worker poll nền để đảm bảo eventual convergence khi event path lag/down.
// Poll cadence hiện tại 3s để cân bằng freshness và I/O overhead.
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

// runPropagationConsumeLoop tiêu thụ event cross-instance để trigger reload near-instant.
// Event chỉ là trigger metadata; source-of-truth vẫn là YAML local file.
func (s *EngineService) runPropagationConsumeLoop(ctx context.Context) {
	if s.subscriber == nil {
		return
	}
	backoff := 2 * time.Second
	const maxBackoff = 30 * time.Second

	subscribe := func() (<-chan policyEntity.PolicyChangedEvent, bool) {
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
			// Case: event thiếu checksum hoặc checksum đã active.
			// Action: skip để tránh reload dư thừa.
			// Mechanism: idempotent gate theo checksum hiện tại.
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

// loadPolicySnapshotFromSource đọc full source và validate contract YAML trước khi swap.
// Function này là guard cuối để chặn config lỗi làm hỏng runtime active policy.
func (s *EngineService) loadPolicySnapshotFromSource(ctx context.Context) (*policyEntity.PolicySet, string, error) {
	if s.sourceAdapter == nil {
		return nil, "", policyErrorx.ErrPolicyInvalid
	}
	rawBytes, meta, err := s.sourceAdapter.ReadCurrent(ctx)
	if err != nil {
		return nil, "", err
	}
	// Case: path không phải .yaml/.yml.
	// Action: reject ngay để khóa contract YAML-only.
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
	next := &policyEntity.PolicySet{Version: version, UpdatedAt: now, Source: meta.Path, ChecksumSHA: hex.EncodeToString(checksum[:]), Policies: map[string]interface{}{"admin_cidr": runtimePolicies.AdminCIDR}, Runtime: runtimePolicies}
	metaKey := strings.TrimSpace(meta.Path) + ":" + strings.TrimSpace(meta.Version) + ":" + fmt.Sprintf("%d", meta.Size)
	return next, metaKey, nil
}

// parseAndCompilePolicies guarantees typed YAML parse and runtime variable compilation.
// It enforces YAML contract before snapshot swap so callers never receive ambiguous variables.
func parseAndCompilePolicies(rawBytes []byte) (policyConfigYAML.PoliciesFile, policyEntity.RuntimePolicies, error) {
	parsed := policyConfigYAML.PoliciesFile{}
	if err := yaml.Unmarshal(rawBytes, &parsed); err != nil {
		return policyConfigYAML.PoliciesFile{}, policyEntity.RuntimePolicies{}, err
	}
	if strings.TrimSpace(parsed.Version) == "" || strings.TrimSpace(parsed.Version) != defaultPolicyVersion {
		return policyConfigYAML.PoliciesFile{}, policyEntity.RuntimePolicies{}, policyErrorx.ErrPolicyInvalid
	}
	allowlist := make([]string, 0, len(parsed.Policies.AdminCIDR.Allowlist))
	for _, item := range parsed.Policies.AdminCIDR.Allowlist {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		allowlist = append(allowlist, trimmed)
	}
	if len(allowlist) == 0 {
		return policyConfigYAML.PoliciesFile{}, policyEntity.RuntimePolicies{}, policyErrorx.ErrPolicyInvalid
	}
	mode := strings.TrimSpace(parsed.Policies.AdminCIDR.Mode)
	if mode == "" {
		mode = "enforce"
	}
	runtimeVariables := policyEntity.RuntimePolicies{AdminCIDR: policyEntity.CompiledAdminCIDRPolicy{Enabled: parsed.Policies.AdminCIDR.Enabled, Mode: mode, Allowlist: allowlist}}
	return parsed, runtimeVariables, nil
}
