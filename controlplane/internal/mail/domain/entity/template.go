package mailEntity

import "time"

type Template struct {
	ID        string
	TenantID  string
	Name      string
	Subject   string
	BodyHTML  string
	BodyText  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CreateTemplateParams struct {
	TenantID string
	Name     string
	Subject  string
	BodyHTML string
	BodyText string
}

type UpdateTemplateParams struct {
	TenantID string
	ID       string
	Name     string
	Subject  string
	BodyHTML string
	BodyText string
}
