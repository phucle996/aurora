package policySvcInterface

import (
	"context"
	policyEntity "controlplane/internal/policyengine/domain/entity"
)

// PolicySourceAdapter owns source read/detect behavior for runtime hot reload.
// Adapter must not apply business validation; it only reports source state changes.
type PolicySourceAdapter interface {
	ReadMeta(ctx context.Context) (policyEntity.PolicySourceMeta, error)
	ReadCurrent(ctx context.Context) ([]byte, policyEntity.PolicySourceMeta, error)
}

// PolicyPropagationNotifier owns cross-instance propagation metadata channel.
// Notify failures are best-effort and must not rollback local applied snapshot.
type PolicyPropagationNotifier interface {
	PublishPolicyChanged(ctx context.Context, event policyEntity.PolicyChangedEvent) error
}

// PolicyEventSubscriber owns cross-instance event consume channel.
// Subscriber is separated from notifier so module layer can fail-fast on required dependencies.
type PolicyEventSubscriber interface {
	SubscribePolicyChanged(ctx context.Context) (<-chan policyEntity.PolicyChangedEvent, error)
}

type EngineService interface {
	Start(ctx context.Context)
	Current(ctx context.Context) (*policyEntity.PolicySet, error)
	Reload(ctx context.Context) (*policyEntity.PolicySet, error)
}
