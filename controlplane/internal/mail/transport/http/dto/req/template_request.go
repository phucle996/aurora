package mailReq

import "encoding/json"

type CreateTemplateRequest struct {
	IdempotencyKey     string          `json:"idempotency_key" binding:"required,min=8,max=128"`
	Name               string          `json:"name" binding:"required"`
	SubjectTemplate    string          `json:"subject_template" binding:"required"`
	TextTemplate       string          `json:"text_template"`
	HTMLTemplate       string          `json:"html_template"`
	VariableSchemaJSON json.RawMessage `json:"variable_schema_json"`
}

type PublishTemplateVersionRequest struct {
	ExpectedRevision   uint64          `json:"expected_revision" binding:"required,min=1"`
	SubjectTemplate    string          `json:"subject_template" binding:"required"`
	TextTemplate       string          `json:"text_template"`
	HTMLTemplate       string          `json:"html_template"`
	VariableSchemaJSON json.RawMessage `json:"variable_schema_json"`
}

type ArchiveTemplateRequest struct {
	ExpectedRevision uint64 `json:"expected_revision" binding:"required,min=1"`
}
