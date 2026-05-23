package iamMetrics

import (
	"strings"
	"sync"

	"controlplane/internal/observability"

	"github.com/prometheus/client_golang/prometheus"
)

var registerOnce sync.Once

func Register(registry *prometheus.Registry, namespace string) error {
	var registerErr error
	registerOnce.Do(func() {
		namespace = normalizeNamespace(namespace)
		if err := registerAuthMetrics(registry, namespace); err != nil {
			registerErr = err
			return
		}
		if err := registerAdminMetrics(registry, namespace); err != nil {
			registerErr = err
			return
		}
		if err := registerRbacMetrics(registry, namespace); err != nil {
			registerErr = err
			return
		}
		if err := registerDeviceCapMetrics(registry, namespace); err != nil {
			registerErr = err
			return
		}
		if err := registerAuditMetrics(registry, namespace); err != nil {
			registerErr = err
			return
		}
	})
	return registerErr
}

func normalizeNamespace(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	if value == "" {
		return "aurora_controlplane"
	}
	return value
}

func init() {
	observability.RegisterModuleMetrics(Register)
}
