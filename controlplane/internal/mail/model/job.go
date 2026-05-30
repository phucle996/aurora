package mailModel

import (
	"time"
)

type MailJobType string

const (
	SendMail      MailJobType = "send_mail"
	BatchSendMail MailJobType = "batch_send_mail"
)

type MailJobPayload struct {
	JobID          string                 `json:"job_id"`
	TenantID       string                 `json:"tenant_id"`
	TemplateID     string                 `json:"template_id"`
	GatewayID      string                 `json:"gateway_id,omitempty"`
	RecipientEmail string                 `json:"recipient_email"`
	Variables      map[string]interface{} `json:"variables"`
	PublishedAt    time.Time              `json:"published_at"`
}
