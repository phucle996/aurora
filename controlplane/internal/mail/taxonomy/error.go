package mailTaxonomy

import "errors"

var (
	ErrInvalidArgument        = errors.New("mail: invalid argument")
	ErrAlreadyExists          = errors.New("mail: already exists")
	ErrWorkspaceNotFound      = errors.New("mail: workspace not found")
	ErrVersionConflict        = errors.New("mail: version conflict")
	ErrOperationInProgress    = errors.New("mail: operation in progress")
	ErrConsumerNotFound       = errors.New("mail: consumer not found")
	ErrTemplateNotFound       = errors.New("mail: template not found")
	ErrTemplateInUse          = errors.New("mail: template is used by an active consumer")
	ErrTemplateSyntax         = errors.New("mail: template syntax error")
	ErrInfrastructureNotFound = errors.New("mail: infrastructure not found")
	ErrInternal               = errors.New("mail: internal server error")
)
