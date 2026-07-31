package dto

import "encoding/json"

type ListPersonalInstancesQuery struct {
	Limit  string `form:"limit"`
	Cursor string `form:"cursor"`
}

type ListPersonalInstanceOperationsQuery struct {
	Limit  string `form:"limit"`
	Cursor string `form:"cursor"`
}

type RenamePersonalInstanceRequest struct {
	Name                    string `json:"name"`
	ExpectedMetadataVersion int64  `json:"expected_metadata_version"`
}

type CreatePersonalInstanceRequest struct {
	Code                string                     `json:"code"`
	Name                string                     `json:"name"`
	BlueprintRevisionID string                     `json:"blueprint_revision_id"`
	InputSchemaSHA256   string                     `json:"input_schema_sha256"`
	Parameters          map[string]json.RawMessage `json:"parameters"`
}

type ResizePersonalInstanceRequest struct {
	ExpectedGeneration int64                      `json:"expected_generation"`
	Resources          map[string]json.RawMessage `json:"resources"`
}

type DeletePersonalInstanceRequest struct {
	ExpectedGeneration int64 `json:"expected_generation"`
}
