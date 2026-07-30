// ============================================================================
// 📂 FILE: internal/observability/pgx_tracer.go - PostgreSQL Query Tracer
// ============================================================================
// Tích hợp OpenTelemetry Tracing và OTel Metrics cho mọi câu truy vấn PostgreSQL
// thông qua pgx QueryTracer interface. Ghi nhận cả Span tracing và dependency latency.
// ============================================================================

package observability

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type pgxQueryTracer struct {
	metrics DependencyRecorder
}

type pgxTraceContextKey struct{}

type pgxTraceContext struct {
	span      trace.Span
	startedAt time.Time
	operation string
}

func NewPGXQueryTracer(metrics DependencyRecorder) pgx.QueryTracer {
	return pgxQueryTracer{metrics: metrics}
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

func (t pgxQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	traceCtx, ok := ctx.Value(pgxTraceContextKey{}).(pgxTraceContext)
	if !ok {
		return
	}

	duration := time.Since(traceCtx.startedAt)
	result, reason := ResultSuccess, ReasonNone
	if data.Err != nil {
		switch {
		case errors.Is(data.Err, pgx.ErrNoRows):
			result, reason = ResultRejected, ReasonEmpty
		case errors.Is(data.Err, context.DeadlineExceeded):
			result, reason = ResultFailure, ReasonTimeout
		case errors.Is(data.Err, context.Canceled):
			result, reason = ResultFailure, ReasonCanceled
		default:
			var pgErr *pgconn.PgError
			if errors.As(data.Err, &pgErr) && strings.HasPrefix(pgErr.Code, "23") {
				result, reason = ResultRejected, ReasonConstraint
			} else if errors.As(data.Err, &pgErr) && (strings.HasPrefix(pgErr.Code, "08") || pgErr.Code == "57P01" || pgErr.Code == "57P02" || pgErr.Code == "57P03") {
				result, reason = ResultFailure, ReasonUnavailable
			} else {
				result, reason = ResultFailure, ReasonInternal
			}
		}
	}
	traceCtx.span.SetAttributes(
		attribute.String("aurora.result", string(result)),
		attribute.String("aurora.reason", string(reason)),
	)
	if result == ResultFailure {
		// SQL errors may contain row values in provider-specific detail fields.
		// The bounded taxonomy keeps traces useful without copying those values.
		traceCtx.span.SetStatus(codes.Error, string(reason))
	}
	// Observe before End so the dependency metric dimensions are attached to
	// this client span, rather than being lost on an already-ended span.
	t.metrics.ObserveDependency(ctx, "postgresql", strings.ToLower(traceCtx.operation), result, reason, duration)
	traceCtx.span.End()
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
