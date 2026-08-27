package iamEntity

import "github.com/google/uuid"

// TenantRuntimeReadAuthorization is the flat Tenant-range authority evaluated
// before ACR mints a Zone runtime-read assertion. It has no Personal fallback.
type TenantRuntimeReadAuthorization struct {
	ActorUserID uuid.UUID
	TenantID    uuid.UUID
	WorkspaceID uuid.UUID
	Permission  string
}
