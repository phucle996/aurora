package observability

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Prometheus struct {
	registry                *prometheus.Registry
	requestTotal            *prometheus.CounterVec
	requestDuration         *prometheus.HistogramVec
	inFlight                prometheus.Gauge
	dependencyDur           *prometheus.HistogramVec
	timeDriftGauge          prometheus.Gauge
	timeSyncStateGauge      *prometheus.GaugeVec
}

var currentPrometheus atomic.Pointer[Prometheus]

var (
	moduleMetricRegistrars []func(registry *prometheus.Registry, namespace string) error
)

func RegisterModuleMetrics(registrar func(registry *prometheus.Registry, namespace string) error) {
	if registrar == nil {
		return
	}
	moduleMetricRegistrars = append(moduleMetricRegistrars, registrar)
}

func registerModuleMetrics(registry *prometheus.Registry, namespace string) error {
	for _, registrar := range moduleMetricRegistrars {
		if err := registrar(registry, namespace); err != nil {
			return err
		}
	}
	return nil
}

func InitPrometheus(namespace string) (*Prometheus, error) {
	namespace = normalizeNamespace(namespace)

	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	requestTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Total number of HTTP requests processed by route/method/status.",
	}, []string{"method", "route", "status"})

	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request latency by route/method/status.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "route", "status"})

	inFlight := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "in_flight_requests",
		Help:      "Current number of in-flight HTTP requests.",
	})

	dependencyDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "dependency",
		Name:      "duration_seconds",
		Help:      "Dependency latency by kind/operation/status.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"kind", "operation", "status"})


	timeDriftGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "system",
		Name:      "time_drift_seconds",
		Help:      "Absolute system time drift in seconds from chrony source.",
	})

	timeSyncStateGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "system",
		Name:      "time_sync_state",
		Help:      "Time sync state as one-hot gauge labels.",
	}, []string{"state"})

	if err := registry.Register(requestTotal); err != nil {
		return nil, err
	}
	if err := registry.Register(requestDuration); err != nil {
		return nil, err
	}
	if err := registry.Register(inFlight); err != nil {
		return nil, err
	}
	if err := registry.Register(dependencyDur); err != nil {
		return nil, err
	}
	if err := registry.Register(timeDriftGauge); err != nil {
		return nil, err
	}
	if err := registry.Register(timeSyncStateGauge); err != nil {
		return nil, err
	}

	if err := registerModuleMetrics(registry, namespace); err != nil {
		return nil, err
	}

	prom := &Prometheus{
		registry:                registry,
		requestTotal:            requestTotal,
		requestDuration:         requestDuration,
		inFlight:                inFlight,
		dependencyDur:           dependencyDur,
		timeDriftGauge:          timeDriftGauge,
		timeSyncStateGauge:      timeSyncStateGauge,
	}
	currentPrometheus.Store(prom)
	return prom, nil
}

func (p *Prometheus) ObserveTimeDrift(seconds float64, state string) {
	if p == nil || p.timeDriftGauge == nil || p.timeSyncStateGauge == nil {
		return
	}
	p.timeDriftGauge.Set(seconds)
	for _, s := range []string{"ok", "warning", "critical", "unknown"} {
		v := 0.0
		if s == state {
			v = 1
		}
		p.timeSyncStateGauge.WithLabelValues(s).Set(v)
	}
}

func CurrentPrometheus() *Prometheus { return currentPrometheus.Load() }
func ClearCurrentPrometheus()        { currentPrometheus.Store(nil) }

func (p *Prometheus) HTTPHandler() http.Handler {
	if p == nil || p.registry == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	}
	return promhttp.HandlerFor(p.registry, promhttp.HandlerOpts{})
}

func (p *Prometheus) IncInFlight() {
	if p != nil && p.inFlight != nil {
		p.inFlight.Inc()
	}
}
func (p *Prometheus) DecInFlight() {
	if p != nil && p.inFlight != nil {
		p.inFlight.Dec()
	}
}

func (p *Prometheus) ObserveRequest(method, route, status string, duration time.Duration) {
	if p == nil || p.requestTotal == nil || p.requestDuration == nil {
		return
	}
	method = strings.TrimSpace(method)
	route = strings.TrimSpace(route)
	status = strings.TrimSpace(status)
	if route == "" {
		route = "/"
	}
	if method == "" {
		method = "UNKNOWN"
	}
	if status == "" {
		status = "0"
	}
	p.requestTotal.WithLabelValues(method, route, status).Inc()
	p.requestDuration.WithLabelValues(method, route, status).Observe(duration.Seconds())
}

func (p *Prometheus) ObserveDB(operation string, duration time.Duration, err error) {
	p.observeDependency("db", operation, duration, err)
}

func (p *Prometheus) ObserveRedis(operation string, duration time.Duration, err error) {
	p.observeDependency("redis", operation, duration, err)
}

func (p *Prometheus) observeDependency(kind, operation string, duration time.Duration, err error) {
	if p == nil || p.dependencyDur == nil {
		return
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "unknown"
	}
	operation = strings.TrimSpace(operation)
	if operation == "" {
		operation = "unknown"
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	p.dependencyDur.WithLabelValues(kind, operation, status).Observe(duration.Seconds())
}

func normalizeNamespace(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	if value == "" {
		return "aurora_controlplane"
	}
	return value
}

func (p *Prometheus) ObserveAdminAction(resource, action, result string) {
	if p == nil || p.requestTotal == nil {
		return
	}
	p.requestTotal.WithLabelValues("admin", resource+"."+action, result).Inc()
}
