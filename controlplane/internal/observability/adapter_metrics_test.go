package observability

import (
	"context"
	"testing"
	"time"

	pkgcontext "controlplane/pkg/context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	redis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type dependencyObservation struct {
	system    string
	operation string
	result    Result
	reason    Reason
	duration  time.Duration
}

type dependencyRecorderSpy struct {
	observations []dependencyObservation
}

func (s *dependencyRecorderSpy) ObserveDependency(_ context.Context, system, operation string, result Result, reason Reason, duration time.Duration) {
	s.observations = append(s.observations, dependencyObservation{
		system: system, operation: operation, result: result, reason: reason, duration: duration,
	})
}

func TestPGXQueryTracerClassifiesConstraintOnce(t *testing.T) {
	recorder := &dependencyRecorderSpy{}
	tracer := NewPGXQueryTracer(recorder)
	ctx := pkgcontext.WithOperation(context.Background(), "managedservice.category.create")
	ctx = tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "WITH inserted AS (INSERT INTO categories VALUES ($1)) SELECT * FROM inserted"})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: &pgconn.PgError{Code: "23505"}})

	if len(recorder.observations) != 1 {
		t.Fatalf("dependency observations = %d, want 1", len(recorder.observations))
	}
	got := recorder.observations[0]
	if got.system != "postgresql" || got.operation != "with" || got.result != ResultRejected || got.reason != ReasonConstraint {
		t.Fatalf("observation = %#v", got)
	}
	if got.duration < 0 {
		t.Fatalf("duration = %v", got.duration)
	}
}

func TestPGXQueryTracerKeepsRejectedConstraintSpanNonError(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	otel.SetTracerProvider(traceProvider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		_ = traceProvider.Shutdown(context.Background())
	})

	tracer := NewPGXQueryTracer(&dependencyRecorderSpy{})
	ctx := pkgcontext.WithOperation(context.Background(), "managedservice.category.create")
	ctx = tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "INSERT INTO categories VALUES ($1)"})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: &pgconn.PgError{Code: "23505"}})

	ended := spanRecorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	if ended[0].Status().Code == codes.Error {
		t.Fatalf("rejected constraint span status = Error, want non-error")
	}
}

func TestRedisHookTreatsNilAsBoundedEmptyResult(t *testing.T) {
	recorder := &dependencyRecorderSpy{}
	hook := NewRedisHook(recorder)
	ctx := pkgcontext.WithOperation(context.Background(), "iam.auth.recover_user_session")
	command := redis.NewStringCmd(ctx, "get", "secret-key-must-not-become-a-label")

	err := hook.ProcessHook(func(context.Context, redis.Cmder) error { return redis.Nil })(ctx, command)
	if err != redis.Nil {
		t.Fatalf("ProcessHook() error = %v, want redis.Nil", err)
	}
	if len(recorder.observations) != 1 {
		t.Fatalf("dependency observations = %d, want 1", len(recorder.observations))
	}
	got := recorder.observations[0]
	if got.system != "redis" || got.operation != "get" || got.result != ResultRejected || got.reason != ReasonEmpty {
		t.Fatalf("observation = %#v", got)
	}
}
