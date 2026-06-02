package observability

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	otelPolicy "controlplane/internal/policyengine/policies/otel"
	"controlplane/pkg/constant"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// DynamicRatioSampler is a custom thread-safe, lock-free sampler that allows updating
// trace sampling ratios dynamically at runtime without restarting the tracer provider.
type DynamicRatioSampler struct {
	ratio uint64
}

// NewDynamicRatioSampler constructs a new DynamicRatioSampler.
func NewDynamicRatioSampler(ratio float64) *DynamicRatioSampler {
	s := &DynamicRatioSampler{}
	s.SetRatio(ratio)
	return s
}

// SetRatio updates the active sampling ratio.
func (s *DynamicRatioSampler) SetRatio(ratio float64) {
	if ratio < 0.0 {
		ratio = 0.0
	} else if ratio > 1.0 {
		ratio = 1.0
	}
	atomic.StoreUint64(&s.ratio, math.Float64bits(ratio))
}

// GetRatio returns the active sampling ratio.
func (s *DynamicRatioSampler) GetRatio() float64 {
	bits := atomic.LoadUint64(&s.ratio)
	return math.Float64frombits(bits)
}

// ShouldSample decides whether a span should be sampled based on the current ratio.
func (s *DynamicRatioSampler) ShouldSample(parameters sdktrace.SamplingParameters) sdktrace.SamplingResult {
	ratio := s.GetRatio()
	return sdktrace.TraceIDRatioBased(ratio).ShouldSample(parameters)
}

// Description returns the description of the sampler.
func (s *DynamicRatioSampler) Description() string {
	return fmt.Sprintf("DynamicRatioSampler{ratio: %0.4f}", s.GetRatio())
}

// OTel manages the OpenTelemetry tracer instance and supports thread-safe hot-swaps.
type OTel struct {
	mu             sync.RWMutex
	tracer         trace.Tracer
	propagator     propagation.TextMapPropagator
	shutdown       func(context.Context) error
	dynamicSampler *DynamicRatioSampler
	activeCfg      *otelPolicy.CompiledPolicy
}

// InitOTel initializes the global OpenTelemetry tracing system using configurations
// provided by the early-booted Policy Engine.
func InitOTel(ctx context.Context, cfg *otelPolicy.CompiledPolicy, serviceName string) (*OTel, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		serviceName = "aurora-controlplane"
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes("",
			attribute.String("service.name", serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: init otel resource: %w", err)
	}

	dynamicSampler := NewDynamicRatioSampler(cfg.SamplingRatio)
	sampler := sdktrace.ParentBased(dynamicSampler)
	options := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(res),
	}

	var spanExporter sdktrace.SpanExporter
	if cfg.Enabled {
		spanExporter, err = newOTLPTraceExporter(ctx, cfg)
		if err != nil {
			return nil, err
		}
		options = append(options, sdktrace.WithBatcher(spanExporter,
			sdktrace.WithBatchTimeout(cfg.BatchTimeout),
			sdktrace.WithExportTimeout(cfg.ExportTimeout),
			sdktrace.WithMaxExportBatchSize(cfg.BatchMaxSize),
			sdktrace.WithMaxQueueSize(cfg.BatchMaxQueue),
		))
	}

	tp := sdktrace.NewTracerProvider(options...)
	propagator := propagation.TraceContext{}
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagator)

	return &OTel{
		tracer:         tp.Tracer(serviceName),
		propagator:     propagator,
		shutdown:       tp.Shutdown,
		dynamicSampler: dynamicSampler,
		activeCfg:      cfg,
	}, nil
}

// Update handles dynamic updates from the Policy Engine thread-safely.
func (o *OTel) Update(ctx context.Context, newCfg *otelPolicy.CompiledPolicy, serviceName string) error {
	if o == nil || newCfg == nil {
		return nil
	}

	o.mu.RLock()
	oldCfg := o.activeCfg
	o.mu.RUnlock()

	// Check if only the SamplingRatio changed
	onlySamplingChanged := false
	if oldCfg != nil && oldCfg.Enabled == newCfg.Enabled &&
		oldCfg.ExporterType == newCfg.ExporterType &&
		oldCfg.Endpoint == newCfg.Endpoint &&
		oldCfg.Insecure == newCfg.Insecure &&
		oldCfg.ExportTimeout == newCfg.ExportTimeout &&
		oldCfg.BatchTimeout == newCfg.BatchTimeout &&
		oldCfg.BatchMaxSize == newCfg.BatchMaxSize &&
		oldCfg.BatchMaxQueue == newCfg.BatchMaxQueue &&
		oldCfg.TLS.Mode == newCfg.TLS.Mode &&
		oldCfg.TLS.CACertPath == newCfg.TLS.CACertPath &&
		oldCfg.TLS.CertPath == newCfg.TLS.CertPath &&
		oldCfg.TLS.KeyPath == newCfg.TLS.KeyPath {

		if oldCfg.SamplingRatio != newCfg.SamplingRatio {
			onlySamplingChanged = true
		}
	}

	if onlySamplingChanged {
		o.dynamicSampler.SetRatio(newCfg.SamplingRatio)
		o.mu.Lock()
		o.activeCfg = newCfg
		o.mu.Unlock()
		return nil
	}

	// Rebuild TracerProvider due to batch parameters or connection updates
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes("",
			attribute.String("service.name", serviceName),
		),
	)
	if err != nil {
		return fmt.Errorf("observability: init resource error during swap: %w", err)
	}

	dynamicSampler := NewDynamicRatioSampler(newCfg.SamplingRatio)
	sampler := sdktrace.ParentBased(dynamicSampler)
	options := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(res),
	}

	var spanExporter sdktrace.SpanExporter
	if newCfg.Enabled {
		spanExporter, err = newOTLPTraceExporter(ctx, newCfg)
		if err != nil {
			return err
		}
		options = append(options, sdktrace.WithBatcher(spanExporter,
			sdktrace.WithBatchTimeout(newCfg.BatchTimeout),
			sdktrace.WithExportTimeout(newCfg.ExportTimeout),
			sdktrace.WithMaxExportBatchSize(newCfg.BatchMaxSize),
			sdktrace.WithMaxQueueSize(newCfg.BatchMaxQueue),
		))
	}

	tp := sdktrace.NewTracerProvider(options...)
	newTracer := tp.Tracer(serviceName)

	o.mu.Lock()
	oldShutdown := o.shutdown
	o.tracer = newTracer
	o.shutdown = tp.Shutdown
	o.dynamicSampler = dynamicSampler
	o.activeCfg = newCfg
	o.mu.Unlock()

	// Graceful Drain: Drain and shut down the old provider in a background routine with a 5s timeout
	if oldShutdown != nil {
		go func() {
			drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = oldShutdown(drainCtx)
		}()
	}

	return nil
}

func newOTLPTraceExporter(ctx context.Context, cfg *otelPolicy.CompiledPolicy) (sdktrace.SpanExporter, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("observability: otlp endpoint is required")
	}

	options := []otlptracegrpc.Option{
		otlptracegrpc.WithDialOption(grpc.WithBlock()),
	}
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		options = append(options, otlptracegrpc.WithEndpointURL(endpoint))
	} else {
		options = append(options, otlptracegrpc.WithEndpoint(strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")))
	}

	if cfg.TLS.Mode == "tls" || cfg.TLS.Mode == "mtls" {
		tlsCreds, err := loadOTelTLSCredentials(cfg.TLS.Mode, cfg.TLS.CACertPath, cfg.TLS.CertPath, cfg.TLS.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("observability: load TLS credentials: %w", err)
		}
		options = append(options, otlptracegrpc.WithTLSCredentials(tlsCreds))
	} else if cfg.Insecure {
		options = append(options, otlptracegrpc.WithTLSCredentials(insecure.NewCredentials()))
	}

	exportCtx, cancel := context.WithTimeout(ctx, cfg.ExportTimeout)
	defer cancel()

	exporter, err := otlptracegrpc.New(exportCtx, options...)
	if err != nil {
		return nil, fmt.Errorf("observability: init otlp trace exporter: %w", err)
	}
	return exporter, nil
}

func loadOTelTLSCredentials(mode string, caCertPath, certPath, keyPath string) (credentials.TransportCredentials, error) {
	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate: %w", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA certificate")
	}

	tlsConfig := &tls.Config{
		RootCAs: certPool,
	}

	if mode == "mtls" && certPath != "" && keyPath != "" {
		clientCert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load client key pair: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	return credentials.NewTLS(tlsConfig), nil
}

func (o *OTel) Shutdown(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.mu.RLock()
	shutdownFn := o.shutdown
	o.mu.RUnlock()

	if shutdownFn == nil {
		return nil
	}
	return shutdownFn(ctx)
}

func (o *OTel) Extract(ctx context.Context, headers http.Header) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if o == nil || o.propagator == nil || headers == nil {
		return ctx
	}
	return o.propagator.Extract(ctx, propagation.HeaderCarrier(headers))
}

func (o *OTel) Inject(ctx context.Context, headers http.Header) {
	if ctx == nil || headers == nil {
		return
	}
	if o == nil || o.propagator == nil {
		return
	}
	o.propagator.Inject(ctx, propagation.HeaderCarrier(headers))
}

func (o *OTel) StartServerSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if o == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	o.mu.RLock()
	tracer := o.tracer
	o.mu.RUnlock()

	if tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	return tracer.Start(ctx, strings.TrimSpace(name), trace.WithSpanKind(trace.SpanKindServer))
}

func ExtractTraceparent(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(constant.HeaderTraceparent))
}
