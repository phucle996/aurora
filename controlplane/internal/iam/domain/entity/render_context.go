package iamEntity

import "github.com/google/uuid"

// PersonalRenderContext is the only business entity carried by the personal
// render workflow. Parallel navigation slices keep the entity flat while the
// HTTP adapter remains responsible for the JSON shape.
type PersonalRenderContext struct {
	UserID            uuid.UUID
	Permissions       []string
	NavigationKeys    []string
	NavigationActions []string
	Capabilities      []string
}

// TenantRenderContext is isolated from PersonalRenderContext so a future
// change in one owner branch cannot silently change the other branch.
type TenantRenderContext struct {
	UserID            uuid.UUID
	TenantID          uuid.UUID
	Permissions       []string
	NavigationKeys    []string
	NavigationActions []string
	Capabilities      []string
}
