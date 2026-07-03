package iamReq

type CreateRoleRequest struct {
	Code          string  `json:"code" binding:"required"`
	Name          string  `json:"name" binding:"required"`
	OwnerTenantID *string `json:"owner_tenant_id,omitempty"`
}

type UpdateRoleRequest struct {
	Name          string  `json:"name"`
	OwnerTenantID *string `json:"owner_tenant_id,omitempty"`
}

type AssignPermissionRequest struct {
	PermissionID string `json:"permission_id"`
}

type AssignUserRoleRequest struct {
	UserID    string  `json:"user_id"`
	ExpiresAt *string `json:"expires_at,omitempty"` // RFC3339 string format
}
