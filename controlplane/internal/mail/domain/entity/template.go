package mailEntity

import "time"

type Template struct {
	ID        string
	Name      string
	Subject   string
	Body      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CreateTemplateParams struct {
	Name     string
	Subject  string
	Body     string
}

type UpdateTemplateParams struct {
	ID       string
	Name     string
	Subject  string
	Body     string
}
