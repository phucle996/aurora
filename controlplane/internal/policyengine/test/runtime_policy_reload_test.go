package policyenginetest

import (
	"context"
	"errors"
	"testing"

	policyerrorx "controlplane/internal/policyengine/errorx"
	policyruntime "controlplane/internal/policyengine/runtime"
	policytypes "controlplane/internal/policyengine/runtime/types"
)

type fakeSourceAdapter struct {
	meta policytypes.PolicySourceMeta
	raw  []byte
	err  error
}

func (f *fakeSourceAdapter) ReadMeta(context.Context) (policytypes.PolicySourceMeta, error) {
	if f.err != nil {
		return policytypes.PolicySourceMeta{}, f.err
	}
	return f.meta, nil
}

func (f *fakeSourceAdapter) ReadCurrent(context.Context) ([]byte, policytypes.PolicySourceMeta, error) {
	if f.err != nil {
		return nil, policytypes.PolicySourceMeta{}, f.err
	}
	return f.raw, f.meta, nil
}

type noopNotifier struct{}

func (noopNotifier) PublishPolicyChanged(context.Context, policytypes.PolicyChangedEvent) error {
	return nil
}
func (noopNotifier) SubscribePolicyChanged(context.Context) (<-chan policytypes.PolicyChangedEvent, error) {
	ch := make(chan policytypes.PolicyChangedEvent)
	close(ch)
	return ch, nil
}

func TestReload_ValidPolicy_NoFallbackValueDrift(t *testing.T) {
	source := &fakeSourceAdapter{
		meta: policytypes.PolicySourceMeta{Path: "runtime/policies/policy.yaml", Version: "1", Size: 1},
		raw: []byte(`version: v1
policies:
  admin_cidr:
    enabled: true
    mode: enforce
    allowlist:
      - 127.0.0.1/32
  rate_limit:
    postauth:
      rules:
        - path: "/api/v1/auth/login"
          capacity: 44
          refill: 55
          period_seconds: 6
    observability:
      sampling_percent:
        throttle: 9
        temporary_isolation: 19
        block: 29
        error: 39
    behavior:
      retry_after_fallback_seconds: 4
      fail_open: true
      bypass_route_patterns:
        - /metrics
        - /api/v1/health/liveness
`),
	}
	service := policyruntime.NewEngineService(nil, source, noopNotifier{}, noopNotifier{})

	snapshot, err := service.Reload(context.Background())
	if err != nil {
		t.Fatalf("expected valid reload, got err: %v", err)
	}

	if len(snapshot.Runtime.RateLimit.PostAuth.Rules) != 1 || snapshot.Runtime.RateLimit.PostAuth.Rules[0].Refill != 55 {
		t.Fatalf("rate values drifted after compile: %+v", snapshot.Runtime.RateLimit)
	}
	if snapshot.Runtime.RateLimit.Observability.SamplingPercent.Error != 39 {
		t.Fatalf("sampling value drifted: %+v", snapshot.Runtime.RateLimit.Observability.SamplingPercent)
	}
	if snapshot.Runtime.RateLimit.Behavior.RetryAfterFallbackSeconds != 4 || !snapshot.Runtime.RateLimit.Behavior.FailOpen {
		t.Fatalf("behavior value drifted: %+v", snapshot.Runtime.RateLimit.Behavior)
	}
}

func TestReload_InvalidRateLimitFields_ReturnPolicyInvalid(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "invalid retry fallback",
			yaml: `version: v1
policies:
  admin_cidr:
    enabled: true
    mode: enforce
    allowlist:
      - 127.0.0.1/32
  rate_limit:
    postauth:
      rules:
        - path: "/api/v1/auth/login"
          capacity: 40
          refill: 40
          period_seconds: 60
    observability:
      sampling_percent:
        throttle: 10
        temporary_isolation: 50
        block: 100
        error: 100
    behavior:
      retry_after_fallback_seconds: 0
      fail_open: false
      bypass_route_patterns:
        - /metrics`,
		},
		{
			name: "invalid sampling",
			yaml: `version: v1
policies:
  admin_cidr:
    enabled: true
    mode: enforce
    allowlist:
      - 127.0.0.1/32
  rate_limit:
    postauth:
      rules:
        - path: "/api/v1/auth/login"
          capacity: 40
          refill: 40
          period_seconds: 60
    observability:
      sampling_percent:
        throttle: 101
        temporary_isolation: 50
        block: 100
        error: 100
    behavior:
      retry_after_fallback_seconds: 2
      fail_open: false
      bypass_route_patterns:
        - /metrics`,
		},
		{name: "empty bypass list", yaml: baseYAMLEmptyBypass()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := &fakeSourceAdapter{
				meta: policytypes.PolicySourceMeta{Path: "runtime/policies/policy.yaml", Version: "1", Size: int64(len(tc.yaml))},
				raw:  []byte(tc.yaml),
			}
			service := policyruntime.NewEngineService(nil, source, noopNotifier{}, noopNotifier{})

			_, err := service.Reload(context.Background())
			if !errors.Is(err, policyerrorx.ErrPolicyInvalid) {
				t.Fatalf("expected ErrPolicyInvalid, got: %v", err)
			}
		})
	}
}

func TestReload_KeepLastKnownGood_WhenNextPolicyInvalid(t *testing.T) {
	valid := []byte(baseYAMLWithOverride("retry_after_fallback_seconds: 2"))
	invalid := []byte(baseYAMLWithOverride("retry_after_fallback_seconds: 0"))

	source := &fakeSourceAdapter{
		meta: policytypes.PolicySourceMeta{Path: "runtime/policies/policy.yaml", Version: "1", Size: int64(len(valid))},
		raw:  valid,
	}
	service := policyruntime.NewEngineService(nil, source, noopNotifier{}, noopNotifier{})

	first, err := service.Reload(context.Background())
	if err != nil {
		t.Fatalf("expected first valid reload, got err: %v", err)
	}

	source.meta = policytypes.PolicySourceMeta{Path: "runtime/policies/policy.yaml", Version: "2", Size: int64(len(invalid))}
	source.raw = invalid

	_, err = service.Reload(context.Background())
	if !errors.Is(err, policyerrorx.ErrPolicyInvalid) {
		t.Fatalf("expected invalid on second reload, got: %v", err)
	}

	current, err := service.Current(context.Background())
	if err != nil {
		t.Fatalf("expected last-known-good snapshot, got err: %v", err)
	}
	if current.ChecksumSHA != first.ChecksumSHA {
		t.Fatalf("expected checksum to keep last-known-good, got current=%s first=%s", current.ChecksumSHA, first.ChecksumSHA)
	}
}

func baseYAMLWithOverride(override string) string {
	return `version: v1
policies:
  admin_cidr:
    enabled: true
    mode: enforce
    allowlist:
      - 127.0.0.1/32
  rate_limit:
    postauth:
      rules:
        - path: "/api/v1/auth/login"
          capacity: 40
          refill: 40
          period_seconds: 60
    observability:
      sampling_percent:
        throttle: 10
        temporary_isolation: 50
        block: 100
        error: 100
    behavior:
      ` + override + `
      fail_open: false
      bypass_route_patterns:
        - /metrics
`
}

func baseYAMLEmptyBypass() string {
	return `version: v1
policies:
  admin_cidr:
    enabled: true
    mode: enforce
    allowlist:
      - 127.0.0.1/32
  rate_limit:
    postauth:
      rules:
        - path: "/api/v1/auth/login"
          capacity: 40
          refill: 40
          period_seconds: 60
    observability:
      sampling_percent:
        throttle: 10
        temporary_isolation: 50
        block: 100
        error: 100
    behavior:
      retry_after_fallback_seconds: 2
      fail_open: false
      bypass_route_patterns: []
`
}


