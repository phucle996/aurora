package dto

type CreateBlueprintRequest struct {
	Code        string            `json:"code"`
	Name        map[string]string `json:"name"`
	Description map[string]string `json:"description"`
	IconKey     string            `json:"icon_key"`
}

type DeleteBlueprintRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}
