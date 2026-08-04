package hierarchyReq

type CreateTenantInvitationRequest struct {
	Identifier   string `json:"identifier" binding:"required"`
	TenantRoleID string `json:"tenant_role_id" binding:"required"`
}

type JoinTenantInvitationRequest struct {
	Token string `json:"token" binding:"required"`
}
