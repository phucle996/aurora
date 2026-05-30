package mailModel

import (
	mailEntity "controlplane/internal/mail/domain/entity"
	"time"
)

type Template struct {
	ID        string    `db:"id"`
	TenantID  string    `db:"tenant_id"`
	Name      string    `db:"name"`
	Subject   string    `db:"subject"`
	BodyHTML  string    `db:"body_html"`
	BodyText  string    `db:"body_text"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func TemplateEntityToModel(e mailEntity.Template) Template {
	return Template{
		ID:        e.ID,
		TenantID:  e.TenantID,
		Name:      e.Name,
		Subject:   e.Subject,
		BodyHTML:  e.BodyHTML,
		BodyText:  e.BodyText,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

func TemplateModelToEntity(m Template) mailEntity.Template {
	return mailEntity.Template{
		ID:        m.ID,
		TenantID:  m.TenantID,
		Name:      m.Name,
		Subject:   m.Subject,
		BodyHTML:  m.BodyHTML,
		BodyText:  m.BodyText,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
