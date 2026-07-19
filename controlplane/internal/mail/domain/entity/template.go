package mailEntity

type TemplateScope string

const (
	TemplateScopePlatform  TemplateScope = "platform"
	TemplateScopeWorkspace TemplateScope = "workspace"
)

type TemplateStatus string

const (
	TemplateActive   TemplateStatus = "active"
	TemplateArchived TemplateStatus = "archived"
)
