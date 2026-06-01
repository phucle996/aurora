package policyenginetest

import (
	"context"
	"errors"
	"testing"

	policyruntime "controlplane/internal/policyengine/runtime"
	policytypes "controlplane/internal/policyengine/runtime/types"
	policyerrorx "controlplane/internal/policyengine/errorx"
	otel "controlplane/internal/policyengine/policies/otel"
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

func (noopNotifier) PublishPolicyChanged(context.Context, policytypes.PolicyChangedEvent) error { return nil }
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
    preauth:
      global_instant:
        max_inflight: 3333
        queue_limit: 7
        retry_after_seconds: 3
      ip:
        capacity: 111
        refill: 22
        period_seconds: 9
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

	if snapshot.Runtime.RateLimit.PreAuth.IP.Capacity != 111 || len(snapshot.Runtime.RateLimit.PostAuth.Rules) != 1 || snapshot.Runtime.RateLimit.PostAuth.Rules[0].Refill != 55 {
		t.Fatalf("rate values drifted after compile: %+v", snapshot.Runtime.RateLimit)
	}
	if snapshot.Runtime.RateLimit.PreAuth.GlobalInstant.MaxInflight != 3333 || snapshot.Runtime.RateLimit.PreAuth.GlobalInstant.QueueLimit != 7 {
		t.Fatalf("global instant values drifted: %+v", snapshot.Runtime.RateLimit.PreAuth.GlobalInstant)
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
		{name: "invalid preauth capacity", yaml: baseYAMLWithOverride("capacity: 0")},
		{name: "invalid global inflight", yaml: baseYAMLWithOverride("max_inflight: 0")},
		{name: "invalid retry fallback", yaml: baseYAMLWithOverride("retry_after_fallback_seconds: 0")},
		{name: "invalid sampling", yaml: baseYAMLWithOverride("throttle: 101")},
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
	valid := []byte(baseYAMLWithOverride("capacity: 120"))
	invalid := []byte(baseYAMLWithOverride("capacity: 0"))

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
    preauth:
      global_instant:
        max_inflight: 2000
        queue_limit: 0
        retry_after_seconds: 1
      ip:
        ` + override + `
        refill: 1200
        period_seconds: 60
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
    preauth:
      global_instant:
        max_inflight: 2000
        queue_limit: 0
        retry_after_seconds: 1
      ip:
        capacity: 1200
        refill: 1200
        period_seconds: 60
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

func TestReload_OTelPolicyReload(t *testing.T) {
	yamlWithOTel := `version: v1
policies:
  admin_cidr:
    enabled: true
    mode: enforce
    allowlist:
      - 127.0.0.1/32
  rate_limit:
    preauth:
      global_instant:
        max_inflight: 2000
        queue_limit: 0
        retry_after_seconds: 1
      ip:
        capacity: 1200
        refill: 1200
        period_seconds: 60
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
      bypass_route_patterns:
        - /metrics
  otel:
    enabled: true
    exporter_type: otlpgrpc
    endpoint: http://localhost:4317
    insecure: true
    sampling_ratio: 0.75
    export_timeout: 4s
    batch_timeout: 3s
    batch_max_size: 256
    batch_max_queue: 1024
    tls:
      mode: disable
`
	source := &fakeSourceAdapter{
		meta: policytypes.PolicySourceMeta{Path: "runtime/policies/policy.yaml", Version: "1", Size: int64(len(yamlWithOTel))},
		raw:  []byte(yamlWithOTel),
	}
	service := policyruntime.NewEngineService(nil, source, noopNotifier{}, noopNotifier{})

	done := make(chan struct{})
	var calledCount int
	var receivedCfg *otel.CompiledPolicy
	service.RegisterOTelHook(func(cfg *otel.CompiledPolicy) {
		calledCount++
		receivedCfg = cfg
		close(done)
	})

	snapshot, err := service.Reload(context.Background())
	if err != nil {
		t.Fatalf("expected valid reload, got err: %v", err)
	}

	otel := snapshot.Runtime.OTel
	if !otel.Enabled || otel.ExporterType != "otlpgrpc" || otel.Endpoint != "http://localhost:4317" {
		t.Fatalf("invalid compiled OTel basic fields: %+v", otel)
	}
	if otel.SamplingRatio != 0.75 || otel.BatchMaxSize != 256 || otel.BatchMaxQueue != 1024 {
		t.Fatalf("invalid compiled OTel batch/sampler fields: %+v", otel)
	}

	// Wait for the hook goroutine to execute
	<-done

	// Verify hook was executed exactly once
	if calledCount != 1 {
		t.Fatalf("expected hook to be called exactly once, got: %d", calledCount)
	}
	if receivedCfg == nil || receivedCfg.SamplingRatio != 0.75 {
		t.Fatalf("expected hook to receive correct compiled OTel config, got: %+v", receivedCfg)
	}
}

func TestReload_OTelInvalidTLSMode(t *testing.T) {
	yamlWithInvalidTLS := `version: v1
policies:
  admin_cidr:
    enabled: true
    mode: enforce
    allowlist:
      - 127.0.0.1/32
  rate_limit:
    preauth:
      global_instant:
        max_inflight: 2000
        queue_limit: 0
        retry_after_seconds: 1
      ip:
        capacity: 1200
        refill: 1200
        period_seconds: 60
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
      bypass_route_patterns:
        - /metrics
  otel:
    enabled: true
    exporter_type: otlpgrpc
    endpoint: http://localhost:4317
    insecure: true
    sampling_ratio: 0.75
    export_timeout: 4s
    batch_timeout: 3s
    batch_max_size: 256
    batch_max_queue: 1024
    tls:
      mode: super-secure-invalid
`
	source := &fakeSourceAdapter{
		meta: policytypes.PolicySourceMeta{Path: "runtime/policies/policy.yaml", Version: "1", Size: int64(len(yamlWithInvalidTLS))},
		raw:  []byte(yamlWithInvalidTLS),
	}
	service := policyruntime.NewEngineService(nil, source, noopNotifier{}, noopNotifier{})

	_, err := service.Reload(context.Background())
	if !errors.Is(err, policyerrorx.ErrPolicyInvalid) {
		t.Fatalf("expected ErrPolicyInvalid for unsupported TLS mode, got: %v", err)
	}
}


