package dto

type GetPersonalCatalogVersionQuery struct {
	ExpectedRevisionID string `form:"expected_revision_id"`
}
