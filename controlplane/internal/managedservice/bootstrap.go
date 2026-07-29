package managedservice

import "context"

// Bootstrap starts runtime side effects owned by the Managed Service module.
//
// The skeleton currently owns no worker, timer, broker consumer, or external
// client. Keeping the lifecycle hook here makes the app-level startup order
// explicit without inventing a fake workflow or persistence dependency.
func (m *Module) Bootstrap(_ context.Context) error {
	return nil
}

// Stop gracefully stops module-owned runtime resources.
func (m *Module) Stop() {}
