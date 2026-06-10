package iamReq

type CreateRoleRequest struct {
	Code          string  `json:"code"`
	Name          string  `json:"name"`
	RoleLevel     int     `json:"role_level"`
	ScopeType     string  `json:"scope_type"`
	OwnerTenantID *string `json:"owner_tenant_id,omitempty"`
}

type UpdateRoleRequest struct {
	Code          string  `json:"code"`
	Name          string  `json:"name"`
	RoleLevel     int     `json:"role_level"`
	ScopeType     string  `json:"scope_type"`
	OwnerTenantID *string `json:"owner_tenant_id,omitempty"`
}

type CreatePermissionRequest struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

type AssignPermissionRequest struct {
	PermissionID string `json:"permission_id"`
}

type AssignUserRoleRequest struct {
	UserID      string  `json:"user_id"`
	ScopeType   string  `json:"scope_type"`
	TenantID    *string `json:"tenant_id,omitempty"`
	WorkspaceID *string `json:"workspace_id,omitempty"`
	ExpiresAt   *string `json:"expires_at,omitempty"` // RFC3339 string format
}
