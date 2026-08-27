package iamEntity

import "github.com/google/uuid"

// PersonalRuntimeReadAuthorization is the flat Personal/platform authority
// evaluated before ACR mints a Zone runtime-read assertion.
type PersonalRuntimeReadAuthorization struct {
	ActorUserID   uuid.UUID
	ActorUsername string
	WorkspaceID   uuid.UUID
	Permission    string
}
