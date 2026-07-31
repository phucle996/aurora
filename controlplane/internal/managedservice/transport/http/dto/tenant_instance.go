package dto

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
