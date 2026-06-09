package observability

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type pgxQueryTracer struct{}

type pgxTraceContextKey struct{}

type pgxTraceContext struct {
	span      trace.Span
	startedAt time.Time
	operation string
}

func NewPGXQueryTracer() pgx.QueryTracer {
	return pgxQueryTracer{}
}

func (pgxQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	operation := normalizeSQLOperation(data.SQL)
	ctx, span := otel.Tracer("aurora-controlplane.db").Start(
		ctx,
		"postgres."+strings.ToLower(operation),
		trace.WithSpanKind(trace.SpanKindClient),
	)
	span.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.operation", operation),
	)

	return context.WithValue(ctx, pgxTraceContextKey{}, pgxTraceContext{
		span:      span,
		startedAt: time.Now(),
		operation: operation,
	})
}

func (pgxQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	traceCtx, ok := ctx.Value(pgxTraceContextKey{}).(pgxTraceContext)
	if !ok {
		return
	}

	duration := time.Since(traceCtx.startedAt)
	if data.Err != nil {
		traceCtx.span.RecordError(data.Err)
		traceCtx.span.SetStatus(codes.Error, data.Err.Error())
	}
	traceCtx.span.End()

	if prom := CurrentPrometheus(); prom != nil {
		prom.ObserveDependency("db", traceCtx.operation, duration, data.Err)
	}
}

func normalizeSQLOperation(sql string) string {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return "UNKNOWN"
	}
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return "UNKNOWN"
	}
	return strings.ToUpper(strings.TrimSpace(fields[0]))
}
