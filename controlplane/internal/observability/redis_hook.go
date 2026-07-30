// ============================================================================
// 📂 FILE: internal/observability/redis_hook.go - Redis Command Tracer Hook
// ============================================================================
// Tích hợp OpenTelemetry Tracing và OTel Metrics cho mọi lệnh Redis
// thông qua go-redis Hook interface. Ghi nhận cả Span tracing và dependency latency.
// ============================================================================

package observability

import (
	"context"
	"errors"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type redisHook struct {
	metrics DependencyRecorder
}

func NewRedisHook(metrics DependencyRecorder) redis.Hook {
	return redisHook{metrics: metrics}
}

func (h redisHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h redisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		operation := strings.ToUpper(strings.TrimSpace(cmd.Name()))
		if operation == "" {
			operation = "UNKNOWN"
		}

		startedAt := time.Now()
		ctx, span := otel.Tracer("aurora-controlplane.redis").Start(
			ctx,
			"redis."+strings.ToLower(operation),
			trace.WithSpanKind(trace.SpanKindClient),
		)
		span.SetAttributes(
			attribute.String("db.system", "redis"),
			attribute.String("db.operation", operation),
		)

		err := next(ctx, cmd)
		metricErr := normalizeRedisError(err)
		result, reason := ResultSuccess, ReasonNone
		if errors.Is(err, redis.Nil) {
			result, reason = ResultRejected, ReasonEmpty
		} else if metricErr != nil {
			switch {
			case errors.Is(metricErr, context.DeadlineExceeded):
				result, reason = ResultFailure, ReasonTimeout
			case errors.Is(metricErr, context.Canceled):
				result, reason = ResultFailure, ReasonCanceled
			default:
				result, reason = ResultFailure, ReasonUnavailable
			}
		}
		span.SetAttributes(
			attribute.String("aurora.result", string(result)),
			attribute.String("aurora.reason", string(reason)),
		)
		if metricErr != nil {
			span.SetStatus(codes.Error, string(reason))
		}
		// The metric recorder enriches the live client span with the same
		// bounded dimensions it exports; end only after that boundary.
		h.metrics.ObserveDependency(ctx, "redis", strings.ToLower(operation), result, reason, time.Since(startedAt))
		span.End()

		return err
	}
}

func (h redisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		startedAt := time.Now()
		ctx, span := otel.Tracer("aurora-controlplane.redis").Start(
			ctx,
			"redis.pipeline",
			trace.WithSpanKind(trace.SpanKindClient),
		)
		span.SetAttributes(
			attribute.String("db.system", "redis"),
			attribute.String("db.operation", "PIPELINE"),
			attribute.Int("redis.pipeline.size", len(cmds)),
		)

		err := next(ctx, cmds)
		metricErr := normalizeRedisError(err)
		result, reason := ResultSuccess, ReasonNone
		if metricErr != nil {
			switch {
			case errors.Is(metricErr, context.DeadlineExceeded):
				result, reason = ResultFailure, ReasonTimeout
			case errors.Is(metricErr, context.Canceled):
				result, reason = ResultFailure, ReasonCanceled
			default:
				result, reason = ResultFailure, ReasonUnavailable
			}
		}
		span.SetAttributes(
			attribute.String("aurora.result", string(result)),
			attribute.String("aurora.reason", string(reason)),
		)
		if metricErr != nil {
			span.SetStatus(codes.Error, string(reason))
		}
		// See ProcessHook: preserve the active span while correlation attributes
		// are attached by the central dependency recorder.
		h.metrics.ObserveDependency(ctx, "redis", "pipeline", result, reason, time.Since(startedAt))
		span.End()

		return err
	}
}

func normalizeRedisError(err error) error {
	if err == nil || errors.Is(err, redis.Nil) {
		return nil
	}
	return err
}
