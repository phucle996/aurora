package dto

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
