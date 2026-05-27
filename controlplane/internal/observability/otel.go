package observability

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"controlplane/internal/config"
	"controlplane/pkg/constant"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// HeaderTraceparent được định nghĩa tập trung ở constant.HeaderTraceparent


type OTel struct {
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	shutdown   func(context.Context) error
}

func InitOTel(ctx context.Context, cfg *config.OTelCfg, defaultServiceName string) (*OTel, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	serviceName := strings.TrimSpace(defaultServiceName)
	if cfg != nil && strings.TrimSpace(cfg.ServiceName) != "" {
		serviceName = strings.TrimSpace(cfg.ServiceName)
	}
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

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(resolveSamplingRatio(cfg)))
	options := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(res),
	}

	if cfg != nil && cfg.Enabled {
		spanExporter, err := newOTLPTraceExporter(ctx, cfg)
		if err != nil {
			return nil, err
		}
		options = append(options, sdktrace.WithBatcher(spanExporter,
			sdktrace.WithBatchTimeout(resolveBatchTimeout(cfg)),
			sdktrace.WithExportTimeout(resolveExportTimeout(cfg)),
			sdktrace.WithMaxExportBatchSize(resolveBatchMaxSize(cfg)),
			sdktrace.WithMaxQueueSize(resolveBatchMaxQueue(cfg)),
		))
	}

	tp := sdktrace.NewTracerProvider(options...)
	propagator := propagation.TraceContext{}
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagator)

	return &OTel{tracer: tp.Tracer(serviceName), propagator: propagator, shutdown: tp.Shutdown}, nil
}

func newOTLPTraceExporter(ctx context.Context, cfg *config.OTelCfg) (sdktrace.SpanExporter, error) {
	endpoint := ""
	if cfg != nil {
		endpoint = strings.TrimSpace(cfg.Endpoint)
	}
	if endpoint == "" {
		return nil, fmt.Errorf("observability: otlp endpoint is required from config")
	}

	options := []otlptracegrpc.Option{
		otlptracegrpc.WithDialOption(grpc.WithBlock()),
	}
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		options = append(options, otlptracegrpc.WithEndpointURL(endpoint))
	} else {
		options = append(options, otlptracegrpc.WithEndpoint(strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")))
	}
	if cfg == nil || cfg.Insecure {
		options = append(options, otlptracegrpc.WithTLSCredentials(insecure.NewCredentials()))
	}

	exportCtx, cancel := context.WithTimeout(ctx, resolveExportTimeout(cfg))
	defer cancel()

	exporter, err := otlptracegrpc.New(exportCtx, options...)
	if err != nil {
		return nil, fmt.Errorf("observability: init otlp trace exporter: %w", err)
	}
	return exporter, nil
}

func resolveSamplingRatio(cfg *config.OTelCfg) float64 {
	if cfg == nil || cfg.SamplingRatio <= 0 || cfg.SamplingRatio > 1 {
		return 1.0
	}
	return cfg.SamplingRatio
}

func resolveExportTimeout(cfg *config.OTelCfg) time.Duration {
	if cfg == nil || cfg.ExportTimeout <= 0 {
		return 5 * time.Second
	}
	return cfg.ExportTimeout
}

func resolveBatchTimeout(cfg *config.OTelCfg) time.Duration {
	if cfg == nil || cfg.BatchTimeout <= 0 {
		return 2 * time.Second
	}
	return cfg.BatchTimeout
}

func resolveBatchMaxSize(cfg *config.OTelCfg) int {
	if cfg == nil || cfg.BatchMaxSize <= 0 {
		return 512
	}
	return cfg.BatchMaxSize
}

func resolveBatchMaxQueue(cfg *config.OTelCfg) int {
	if cfg == nil || cfg.BatchMaxQueue <= 0 {
		return 2048
	}
	return cfg.BatchMaxQueue
}

func (o *OTel) Shutdown(ctx context.Context) error {
	if o == nil || o.shutdown == nil {
		return nil
	}
	return o.shutdown(ctx)
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
	if o == nil || o.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	return o.tracer.Start(ctx, strings.TrimSpace(name), trace.WithSpanKind(trace.SpanKindServer))
}

func ExtractTraceparent(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(constant.HeaderTraceparent))
}
