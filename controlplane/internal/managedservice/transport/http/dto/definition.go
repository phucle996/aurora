package dto

type CreateDefinitionRequest struct {
	CategoryID  string            `json:"category_id"`
	Code        string            `json:"code"`
	Name        map[string]string `json:"name"`
	Description map[string]string `json:"description"`
	IconKey     string            `json:"icon_key"`
}

type UpdateDefinitionRequest struct {
	ExpectedVersion int64             `json:"expected_version"`
	Name            map[string]string `json:"name"`
	Description     map[string]string `json:"description"`
	IconKey         string            `json:"icon_key"`
}

type RetireDefinitionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}
