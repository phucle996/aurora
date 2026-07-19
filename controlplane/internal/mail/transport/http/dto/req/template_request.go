package mailReq

type CreateTemplateRequest struct {
	Code            string `json:"code" binding:"required,min=3,max=63"`
	Name            string `json:"name" binding:"required"`
	SubjectTemplate string `json:"subject_template" binding:"required"`
	HTMLTemplate    string `json:"html_template" binding:"required"`
}

type PublishTemplateVersionRequest struct {
	ExpectedRevision uint64 `json:"expected_revision" binding:"required,min=1"`
	SubjectTemplate  string `json:"subject_template" binding:"required"`
	HTMLTemplate     string `json:"html_template" binding:"required"`
}

type DeleteTemplateRequest struct {
	ExpectedRevision uint64 `json:"expected_revision" binding:"required,min=1"`
}
