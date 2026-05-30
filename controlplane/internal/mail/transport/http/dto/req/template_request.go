package mailReq

type CreateTemplateRequest struct {
	Name     string `json:"name" binding:"required"`
	Subject  string `json:"subject" binding:"required"`
	BodyHTML string `json:"body_html" binding:"required"`
	BodyText string `json:"body_text"`
}

type UpdateTemplateRequest struct {
	Name     string `json:"name" binding:"required"`
	Subject  string `json:"subject" binding:"required"`
	BodyHTML string `json:"body_html" binding:"required"`
	BodyText string `json:"body_text"`
}
