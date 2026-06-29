package mailModel

import (
	mailEntity "controlplane/internal/mail/domain/entity"
	"time"
)

type Template struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	Subject   string    `db:"subject"`
	Body      string    `db:"body"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func TemplateEntityToModel(e mailEntity.Template) Template {
	return Template{
		ID:        e.ID,
		Name:      e.Name,
		Subject:   e.Subject,
		Body:      e.Body,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

func TemplateModelToEntity(m Template) mailEntity.Template {
	return mailEntity.Template{
		ID:        m.ID,
		Name:      m.Name,
		Subject:   m.Subject,
		Body:      m.Body,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
