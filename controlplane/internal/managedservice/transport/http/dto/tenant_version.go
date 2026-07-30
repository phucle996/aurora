package dto

type GetTenantCatalogVersionQuery struct {
	ExpectedRevisionID string `form:"expected_revision_id"`
}
