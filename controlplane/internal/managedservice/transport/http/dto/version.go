package dto

type CreateVersionRequest struct {
	DefinitionID   string            `json:"definition_id"`
	Code           string            `json:"code"`
	DisplayVersion string            `json:"display_version"`
	Name           map[string]string `json:"name"`
	Description    map[string]string `json:"description"`
	IconKey        string            `json:"icon_key"`
}

type UpdateVersionRequest struct {
	ExpectedVersion int64             `json:"expected_version"`
	DisplayVersion  string            `json:"display_version"`
	Name            map[string]string `json:"name"`
	Description     map[string]string `json:"description"`
	IconKey         string            `json:"icon_key"`
}

type ChangeVersionStateRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}
