package dto

type CreateCategoryRequest struct {
	Code        string            `json:"code"`
	Name        map[string]string `json:"name"`
	Description map[string]string `json:"description"`
	IconKey     string            `json:"icon_key"`
}

type UpdateCategoryRequest struct {
	ExpectedVersion int64             `json:"expected_version"`
	Name            map[string]string `json:"name"`
	Description     map[string]string `json:"description"`
	IconKey         string            `json:"icon_key"`
}

type RetireCategoryRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}
