package policyruntime

import (
	"context"

	policytypes "controlplane/internal/policyengine/runtime/types"
)

type PolicySourceAdapter interface {
	ReadMeta(ctx context.Context) (policytypes.PolicySourceMeta, error)
	ReadCurrent(ctx context.Context) ([]byte, policytypes.PolicySourceMeta, error)
}

type PolicyPropagationNotifier interface {
	PublishPolicyChanged(ctx context.Context, event policytypes.PolicyChangedEvent) error
}

type PolicyEventSubscriber interface {
	SubscribePolicyChanged(ctx context.Context) (<-chan policytypes.PolicyChangedEvent, error)
}

