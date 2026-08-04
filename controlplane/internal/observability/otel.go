package observability

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"controlplane/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// OTel quản lý các đối tượng Tracer và Meter của OpenTelemetry.
type OTel struct {
	tracer     trace.Tracer
	metrics    *Metrics
	propagator propagation.TextMapPropagator
	shutdown   func(context.Context) error
}

func LocalHostname() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return "unknown"
	}
	return hostname
}

// [COMMENT]: InitOTel khởi tạo hệ thống tracing và metrics của OpenTelemetry tĩnh lúc khởi động.
// Nhận cấu hình OTelCfg và AppInfo (chứa Tên và Phiên bản ứng dụng) từ gói config toàn cục
func InitOTel(ctx context.Context, cfg *config.OTelCfg, appInfo config.AppInfo) (*OTel, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	serviceName := strings.TrimSpace(appInfo.Name)
	if serviceName == "" {
		serviceName = "controlplane"
	}
	serviceVersion := strings.TrimSpace(appInfo.Version)
	if serviceVersion == "" {
		serviceVersion = "dev"
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes("",
			attribute.String("service.name", serviceName),
			attribute.String("service.version", serviceVersion),
			attribute.String("service.instance.id", LocalHostname()),
			attribute.String("aurora.component", "controlplane"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: init otel resource: %w", err)
	}

	propagator := propagation.TraceContext{}
	if !cfg.Enabled {
		return NewNoopOTel(serviceName), nil
	}

	// Both exporters must be ready before either global provider is installed.
	// This keeps fail-close real and prevents a half-observable process.
	spanExporter, err := newOTLPTraceExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}
	metricExporter, err := newOTLPMetricExporter(ctx, cfg)
	if err != nil {
		_ = spanExporter.Shutdown(ctx)
		return nil, err
	}

	// [1] TRACING SETUP
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SamplingRatio))
	traceOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(spanExporter,
			sdktrace.WithBatchTimeout(cfg.BatchTimeout),
			sdktrace.WithExportTimeout(cfg.ExportTimeout),
			sdktrace.WithMaxExportBatchSize(cfg.BatchMaxSize),
			sdktrace.WithMaxQueueSize(cfg.BatchMaxQueue),
		),
	}
	tp := sdktrace.NewTracerProvider(traceOptions...)

	// [2] METRICS SETUP
	reader := sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(30*time.Second))
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
		sdkmetric.WithView(durationHistogramView("aurora_controlplane_http_request_duration_seconds")),
		sdkmetric.WithView(durationHistogramView("aurora_controlplane_workflow_duration_seconds")),
		sdkmetric.WithView(durationHistogramView("aurora_controlplane_dependency_duration_seconds")),
	)
	metrics, err := NewMetrics(meterProvider)
	if err != nil {
		_ = meterProvider.Shutdown(ctx)
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("observability: create metric instruments: %w", err)
	}

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagator)

	shutdownFn := func(sCtx context.Context) error {
		var errs []string
		if err := meterProvider.Shutdown(sCtx); err != nil {
			errs = append(errs, fmt.Sprintf("meter: %v", err))
		}
		if err := tp.Shutdown(sCtx); err != nil {
			errs = append(errs, fmt.Sprintf("tracer: %v", err))
		}
		if len(errs) > 0 {
			return fmt.Errorf("otel shutdown error: %s", strings.Join(errs, ", "))
		}
		return nil
	}

	return &OTel{
		tracer:     tp.Tracer(serviceName),
		metrics:    metrics,
		propagator: propagator,
		shutdown:   shutdownFn,
	}, nil
}

func NewNoopOTel(serviceName string) *OTel {
	provider := trace.NewNoopTracerProvider()
	meterProvider := metricnoop.NewMeterProvider()
	propagator := propagation.TraceContext{}
	otel.SetTracerProvider(provider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagator)
	return &OTel{
		tracer:     provider.Tracer(serviceName),
		metrics:    NewNoopMetrics(),
		propagator: propagator,
		shutdown:   func(context.Context) error { return nil },
	}
}

func durationHistogramView(name string) sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{Name: name},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
			Boundaries: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}},
	)
}

func (o *OTel) Metrics() *Metrics {
	if o == nil || o.metrics == nil {
		return NewNoopMetrics()
	}
	return o.metrics
}

func (o *OTel) WorkflowRecorder(module string) WorkflowRecorder {
	return o.Metrics().ForModule(module)
}

func (o *OTel) DependencyRecorder() DependencyRecorder {
	return o.Metrics()
}

func (o *OTel) CacheRecorder() CacheRecorder {
	return o.Metrics()
}

func newOTLPTraceExporter(ctx context.Context, cfg *config.OTelCfg) (sdktrace.SpanExporter, error) {
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

	// Đọc trực tiếp cấu hình TLS dạng phẳng
	if cfg.TLSMode == "tls" || cfg.TLSMode == "mtls" {
		tlsCreds, err := loadOTelTLSCredentials(cfg.TLSMode, cfg.CACertPath, cfg.CertPath, cfg.KeyPath)
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

func newOTLPMetricExporter(ctx context.Context, cfg *config.OTelCfg) (sdkmetric.Exporter, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("observability: otlp endpoint is required for metrics")
	}

	options := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithDialOption(grpc.WithBlock()),
	}
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		options = append(options, otlpmetricgrpc.WithEndpointURL(endpoint))
	} else {
		options = append(options, otlpmetricgrpc.WithEndpoint(strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")))
	}

	// Đọc trực tiếp cấu hình TLS dạng phẳng
	if cfg.TLSMode == "tls" || cfg.TLSMode == "mtls" {
		tlsCreds, err := loadOTelTLSCredentials(cfg.TLSMode, cfg.CACertPath, cfg.CertPath, cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("observability: load TLS credentials for metrics: %w", err)
		}
		options = append(options, otlpmetricgrpc.WithTLSCredentials(tlsCreds))
	} else if cfg.Insecure {
		options = append(options, otlpmetricgrpc.WithTLSCredentials(insecure.NewCredentials()))
	}

	exportCtx, cancel := context.WithTimeout(ctx, cfg.ExportTimeout)
	defer cancel()

	exporter, err := otlpmetricgrpc.New(exportCtx, options...)
	if err != nil {
		return nil, fmt.Errorf("observability: init otlp metric exporter: %w", err)
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

// Shutdown thực hiện xả (flush) dữ liệu trong bộ nhớ đệm và đóng các kết nối của OTel an toàn.
// Được gọi lúc ứng dụng tắt (graceful shutdown) để tránh mất mát vết (traces) và chỉ số (metrics).
func (o *OTel) Shutdown(ctx context.Context) error {
	if o == nil || o.shutdown == nil {
		return nil
	}
	return o.shutdown(ctx)
}

// Extract giải nén thông tin trace context (traceparent) từ HTTP Request Headers vào Go Context.
// Giúp liên kết vết phân tán từ client hoặc service upstream gửi đến.
func (o *OTel) Extract(ctx context.Context, headers http.Header) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if o == nil || o.propagator == nil || headers == nil {
		return ctx
	}
	return o.propagator.Extract(ctx, propagation.HeaderCarrier(headers))
}

// Inject nạp thông tin trace context từ Go Context hiện tại vào HTTP Headers trước khi gửi request đi.
// Giúp truyền vết phân tán tới các service downstream khác.
func (o *OTel) Inject(ctx context.Context, headers http.Header) {
	if ctx == nil || headers == nil {
		return
	}
	if o == nil || o.propagator == nil {
		return
	}
	o.propagator.Inject(ctx, propagation.HeaderCarrier(headers))
}

// StartServerSpan khởi tạo và bắt đầu một Span mới với vai trò xử lý yêu cầu phía Server (SpanKindServer).
// Được sử dụng bởi các middleware để ghi vết toàn bộ vòng đời xử lý request trên controlplane.
func (o *OTel) StartServerSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if o == nil || o.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	return o.tracer.Start(ctx, strings.TrimSpace(name), trace.WithSpanKind(trace.SpanKindServer))
}

// ExtractTraceparent là hàm tiện ích giúp lấy trực tiếp chuỗi traceparent từ Header của HTTP Request.
func ExtractTraceparent(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get("traceparent"))
}
