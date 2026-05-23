package iamReq

type CreateRoleRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type UpdateRoleRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
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
	UserID string `json:"user_id"`
}
