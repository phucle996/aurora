package mailReq

type CreateTemplateRequest struct {
	IdempotencyKey  string `json:"idempotency_key" binding:"required,min=8,max=128"`
	Name            string `json:"name" binding:"required"`
	SubjectTemplate string `json:"subject_template" binding:"required"`
	HTMLTemplate    string `json:"html_template" binding:"required"`
}

type PublishTemplateVersionRequest struct {
	ExpectedRevision uint64 `json:"expected_revision" binding:"required,min=1"`
	SubjectTemplate  string `json:"subject_template" binding:"required"`
	HTMLTemplate     string `json:"html_template" binding:"required"`
}

type ArchiveTemplateRequest struct {
	ExpectedRevision uint64 `json:"expected_revision" binding:"required,min=1"`
}
