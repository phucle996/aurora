// ============================================================================
// 📂 FILE: policies/prometheus/compiler_test.go - Unit Tests Cho Trình Biên Dịch
// ============================================================================

package prometheus

import (
	"errors"
	"testing"
	"time"
)

func TestCompile_Success(t *testing.T) {
	raw := PrometheusPolicy{
		Enabled:      true,
		FailStrategy: "fail_open",
		QueryClient: QueryClientPolicy{
			Enabled:      true,
			BaseURL:      "http://127.0.0.1:9090",
			QueryTimeout: "5s",
			DefaultStep:  "15s",
		},
		ExposeMetric: ExposePolicy{
			Enabled:   true,
			RoutePath: "/metrics",
		},
	}

	compiled, err := Compile(raw)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if !compiled.Enabled {
		t.Error("expected enabled to be true")
	}
	if compiled.FailStrategy != "fail_open" {
		t.Errorf("expected fail_strategy to be 'fail_open', got '%s'", compiled.FailStrategy)
	}
	if !compiled.QueryClient.Enabled {
		t.Error("expected query_client enabled to be true")
	}
	if compiled.QueryClient.BaseURL != "http://127.0.0.1:9090" {
		t.Errorf("expected base_url to match, got '%s'", compiled.QueryClient.BaseURL)
	}
	if compiled.QueryClient.QueryTimeout != 5*time.Second {
		t.Errorf("expected query_timeout to be 5s, got %v", compiled.QueryClient.QueryTimeout)
	}
	if compiled.QueryClient.DefaultStep != 15*time.Second {
		t.Errorf("expected default_step to be 15s, got %v", compiled.QueryClient.DefaultStep)
	}
	if !compiled.ExposeMetric.Enabled {
		t.Error("expected expose_metrics enabled to be true")
	}
	if compiled.ExposeMetric.RoutePath != "/metrics" {
		t.Errorf("expected route_path to be '/metrics', got '%s'", compiled.ExposeMetric.RoutePath)
	}
}

func TestCompile_FailStrategy_StrictValidation(t *testing.T) {
	raw := PrometheusPolicy{
		Enabled:      true,
		FailStrategy: "invalid_strategy", // Must fail strictly
	}

	_, err := Compile(raw)
	if err == nil {
		t.Fatal("expected error for invalid fail_strategy, got none")
	}
	if !errors.Is(err, ErrInvalidFailStrategy) {
		t.Errorf("expected ErrInvalidFailStrategy, got: %v", err)
	}
}

func TestCompile_QueryClient_InvalidURL(t *testing.T) {
	raw := PrometheusPolicy{
		Enabled:      true,
		FailStrategy: "fail_close",
		QueryClient: QueryClientPolicy{
			Enabled:      true,
			BaseURL:      "invalid-url-no-scheme",
			QueryTimeout: "5s",
			DefaultStep:  "15s",
		},
	}

	_, err := Compile(raw)
	if err == nil {
		t.Fatal("expected error for invalid URL, got none")
	}
	if !errors.Is(err, ErrInvalidBaseURL) {
		t.Errorf("expected ErrInvalidBaseURL, got: %v", err)
	}
}

func TestCompile_QueryClient_InvalidTimeout(t *testing.T) {
	raw := PrometheusPolicy{
		Enabled:      true,
		FailStrategy: "fail_open",
		QueryClient: QueryClientPolicy{
			Enabled:      true,
			BaseURL:      "http://localhost:9090",
			QueryTimeout: "5sec", // invalid duration suffix
			DefaultStep:  "15s",
		},
	}

	_, err := Compile(raw)
	if err == nil {
		t.Fatal("expected error for invalid timeout duration, got none")
	}
}

func TestCompile_ExposeMetric_InvalidRoute(t *testing.T) {
	raw := PrometheusPolicy{
		Enabled:      true,
		FailStrategy: "fail_open",
		ExposeMetric: ExposePolicy{
			Enabled:   true,
			RoutePath: "metrics", // missing starting slash
		},
	}

	_, err := Compile(raw)
	if err == nil {
		t.Fatal("expected error for invalid route path, got none")
	}
	if !errors.Is(err, ErrInvalidRoutePath) {
		t.Errorf("expected ErrInvalidRoutePath, got: %v", err)
	}
}
