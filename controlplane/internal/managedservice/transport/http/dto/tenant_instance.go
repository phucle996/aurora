package dto

import "encoding/json"

type ListTenantInstancesQuery struct {
	Limit  string `form:"limit"`
	Cursor string `form:"cursor"`
}

type ListTenantInstanceOperationsQuery struct {
	Limit  string `form:"limit"`
	Cursor string `form:"cursor"`
}

type RenameTenantInstanceRequest struct {
	Name                    string `json:"name"`
	ExpectedMetadataVersion int64  `json:"expected_metadata_version"`
}

type CreateTenantInstanceRequest struct {
	Code                string                     `json:"code"`
	Name                string                     `json:"name"`
	BlueprintRevisionID string                     `json:"blueprint_revision_id"`
	InputSchemaSHA256   string                     `json:"input_schema_sha256"`
	Parameters          map[string]json.RawMessage `json:"parameters"`
}

type ResizeTenantInstanceRequest struct {
	ExpectedGeneration int64                      `json:"expected_generation"`
	Resources          map[string]json.RawMessage `json:"resources"`
}

type DeleteTenantInstanceRequest struct {
	ExpectedGeneration int64 `json:"expected_generation"`
}
