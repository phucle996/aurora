package dto

type ListPersonalCatalogQuery struct {
	Limit  string `form:"limit"`
	Cursor string `form:"cursor"`
}
