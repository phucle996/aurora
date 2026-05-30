package mailReq

type PublishMailJobRequest struct {
	TemplateID     string                 `json:"template_id" binding:"required"`
	GatewayID      string                 `json:"gateway_id"`
	RecipientEmail string                 `json:"recipient_email" binding:"required,email"`
	Variables      map[string]interface{} `json:"variables"`
}
