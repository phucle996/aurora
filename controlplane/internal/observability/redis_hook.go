package observability

import (
	"context"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type redisHook struct{}

func NewRedisHook() redis.Hook {
	return redisHook{}
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
		if metricErr != nil {
			span.RecordError(metricErr)
			span.SetStatus(codes.Error, metricErr.Error())
		}
		span.End()

		if prom := CurrentPrometheus(); prom != nil {
			prom.ObserveRedis(operation, time.Since(startedAt), metricErr)
		}

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
		if metricErr != nil {
			span.RecordError(metricErr)
			span.SetStatus(codes.Error, metricErr.Error())
		}
		span.End()

		if prom := CurrentPrometheus(); prom != nil {
			prom.ObserveRedis("PIPELINE", time.Since(startedAt), metricErr)
		}

		return err
	}
}

func normalizeRedisError(err error) error {
	if err == nil || err == redis.Nil {
		return nil
	}
	return err
}
