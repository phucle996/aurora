package policyengine

import (
	"context"
	"errors"

	"controlplane/internal/config"
	policyAdapter "controlplane/internal/policyengine/adapter"
	policyruntime "controlplane/internal/policyengine/runtime"

	goredis "github.com/redis/go-redis/v9"
)

type Module struct {
	EngineService *policyruntime.EngineService
	workerCancel  context.CancelFunc
}

// NewModule provisions policyengine runtime dependencies và start worker lifecycle.
//
// CONTRACT:
// - Fail-fast ở đây cho dependency bắt buộc (Redis, source adapter, notifier/subscriber).
// - Service constructor chỉ nhận dependency; bootstrap goroutine thuộc ownership của module.
// - Lỗi tại đây phải bubble lên app-layer để caller quyết định dừng process.
func NewModule(cfg *config.Config, rds *goredis.Client) (*Module, error) {
	// Case: thiếu Redis client cho cross-instance propagation.
	// Action: fail-fast ngay tại module boundary, không degrade ngầm.
	if rds == nil {
		return nil, errors.New("policyengine: redis client is required")
	}
	// Source adapter là YAML SoT reader; nếu thiếu thì engine không thể reload runtime policy.
	source := policyAdapter.NewYAMLFileSourceAdapter("runtime/policies/policy.yaml")
	if source == nil {
		return nil, errors.New("policyengine: source adapter is required")
	}
	// Notifier/subscriber dùng chung Redis Pub/Sub implementation để giảm indirection.
	notifier := policyruntime.NewRedisPubSubNotifier(rds, "policyengine.policy.changed.v1")
	if notifier == nil {
		return nil, errors.New("policyengine: propagation notifier is required")
	}
	subscriber := notifier
	if subscriber == nil {
		return nil, errors.New("policyengine: event subscriber is required")
	}
	service := policyruntime.NewEngineService(cfg, source, notifier, subscriber)
	if service == nil {
		return nil, errors.New("policyengine: engine service is required")
	}
	// Worker lifecycle phải gắn context cancel để Stop() dừng sạch background loops.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	service.Start(workerCtx)
	if _, err := service.Reload(workerCtx); err != nil {
		workerCancel()
		return nil, err
	}
	return &Module{
		EngineService: service,
		workerCancel:  workerCancel,
	}, nil
}

// Stop cancels policyengine background workers theo lifecycle của module graph.
// Nil-safe để caller có thể gọi nhiều lần mà không panic.
func (m *Module) Stop() {
	if m == nil {
		return
	}
	if m.workerCancel != nil {
		m.workerCancel()
		m.workerCancel = nil
	}
}
