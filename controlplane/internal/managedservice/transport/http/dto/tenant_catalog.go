package dto

type ListTenantCatalogQuery struct {
	Limit  string `form:"limit"`
	Cursor string `form:"cursor"`
}
