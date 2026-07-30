package dto

import "encoding/json"

type DraftArtifactRequest struct {
	TemplateYAML             string          `json:"template_yaml"`
	ContractVersion          string          `json:"contract_version"`
	ComponentContract        json.RawMessage `json:"component_contract"`
	InputSchema              json.RawMessage `json:"input_schema"`
	UISchema                 json.RawMessage `json:"ui_schema"`
	SafeObservedOutputSchema json.RawMessage `json:"safe_observed_output_schema"`
	ZoneSelector             json.RawMessage `json:"zone_selector"`
	CapabilityRequirement    json.RawMessage `json:"capability_requirement"`
}

type CreateDraftRequest struct{ DraftArtifactRequest }

type PatchDraftRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
	DraftArtifactRequest
}

type ValidateDraftRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
	DraftArtifactRequest
}

type PublishDraftRequest struct {
	ExpectedVersion        int64  `json:"expected_version"`
	ExpectedBundleSHA256   string `json:"expected_bundle_sha256"`
	ExpectedContractSHA256 string `json:"expected_contract_sha256"`
}

type RetireRevisionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}
type DeleteDraftRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}
