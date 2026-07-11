package iamReq

type CreateRoleRequest struct {
	Code          string   `json:"code" binding:"required"`
	Name          string   `json:"name" binding:"required"`
	Description   string   `json:"description"`
	RoleLevel     int      `json:"role_level" binding:"min=0,max=99"`
	Scope         string   `json:"scope" binding:"required,oneof=platform tenant"`
	PermissionIDs []string `json:"permission_ids"`
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

type AssignUserRolePlatformRequest struct {
	UserID string `json:"user_id" binding:"required"`
	RoleID string `json:"role_id" binding:"required"`
}

type UpdateRolePlatformReq struct {
	Name          string   `json:"name" binding:"required,min=1,max=100"`
	Description   string   `json:"description" binding:"max=255"`
	PermissionIDs []string `json:"permission_ids"`
}
