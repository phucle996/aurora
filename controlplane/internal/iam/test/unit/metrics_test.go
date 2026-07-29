package unit

import (
	"context"
	"errors"
	"testing"
	"time"

	iamMetrics "controlplane/internal/iam/metrics"

	"go.opentelemetry.io/otel/metric/noop"
)

func TestIAMMetricsInitializationAndRecording(t *testing.T) {
	iamMetrics.Init(noop.NewMeterProvider())
	iamMetrics.ServiceCall(context.Background(), iamMetrics.OutcomeSuccess)
	iamMetrics.Downstream(context.Background(), iamMetrics.KindRepo, "postgres", iamMetrics.OutcomeSuccess, time.Millisecond, nil)
	iamMetrics.Downstream(context.Background(), iamMetrics.KindCacheEngineL2, "redis", iamMetrics.OutcomeFailureUnknown, time.Millisecond, errors.New("unavailable"))
}
