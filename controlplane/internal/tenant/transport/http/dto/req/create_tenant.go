package requestdto

type CreateTenantRequest struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
}
