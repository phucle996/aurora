package mailTaxonomy

import "errors"

var (
	ErrInvalidArgument     = errors.New("mail: invalid argument")
	ErrAlreadyExists       = errors.New("mail: already exists")
	ErrWorkspaceNotFound   = errors.New("mail: workspace not found")
	ErrVersionConflict     = errors.New("mail: version conflict")
	ErrIdempotencyConflict = errors.New("mail: idempotency key reused with different request")
	ErrConsumerNotFound    = errors.New("mail: consumer not found")
	ErrTemplateNotFound    = errors.New("mail: template not found")
	ErrTemplateSyntax      = errors.New("mail: template syntax error")
	ErrInternal            = errors.New("mail: internal server error")
)
