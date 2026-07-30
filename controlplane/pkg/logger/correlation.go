package logger

import (
	"context"
	"strings"
	"sync"
)

type correlationContextKey struct{}

// CorrelationSnapshot is request-scoped diagnostic state. It never carries
// owner or resource identity, so it is safe to reuse across metric, trace and
// log correlation without creating high-cardinality metric dimensions.
type CorrelationSnapshot struct {
	Module    string
	Operation string
	Result    string
	Reason    string
	Observed  bool
}

type correlationState struct {
	mu       sync.RWMutex
	snapshot CorrelationSnapshot
}

// WithCorrelation installs one synchronized carrier at the transport root.
// Child contexts share the same carrier, allowing a service outcome to be
// observed later by access logging after the service has returned.
func WithCorrelation(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(correlationContextKey{}).(*correlationState); ok {
		return ctx
	}
	return context.WithValue(ctx, correlationContextKey{}, &correlationState{})
}

func SetCorrelationOperation(ctx context.Context, operation string) {
	if ctx == nil {
		return
	}
	state, ok := ctx.Value(correlationContextKey{}).(*correlationState)
	if !ok || state == nil {
		return
	}
	state.mu.Lock()
	state.snapshot.Operation = strings.TrimSpace(operation)
	state.mu.Unlock()
}

func SetCorrelationOutcome(ctx context.Context, module, operation, result, reason string) {
	if ctx == nil {
		return
	}
	state, ok := ctx.Value(correlationContextKey{}).(*correlationState)
	if !ok || state == nil {
		return
	}
	state.mu.Lock()
	state.snapshot = CorrelationSnapshot{
		Module:    strings.TrimSpace(module),
		Operation: strings.TrimSpace(operation),
		Result:    strings.TrimSpace(result),
		Reason:    strings.TrimSpace(reason),
		Observed:  true,
	}
	state.mu.Unlock()
}

func CorrelationFromContext(ctx context.Context) (CorrelationSnapshot, bool) {
	if ctx == nil {
		return CorrelationSnapshot{}, false
	}
	state, ok := ctx.Value(correlationContextKey{}).(*correlationState)
	if !ok || state == nil {
		return CorrelationSnapshot{}, false
	}
	state.mu.RLock()
	snapshot := state.snapshot
	state.mu.RUnlock()
	return snapshot, true
}
