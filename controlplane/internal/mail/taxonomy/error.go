package mailTaxonomy

import "errors"

var (
	ErrInvalidArgument       = errors.New("mail: invalid argument")
	ErrConsumerNotFound      = errors.New("mail: consumer not found")
	ErrTemplateNotFound      = errors.New("mail: template not found")
	ErrTemplateSyntax        = errors.New("mail: template syntax error")
	ErrGatewayNotFound       = errors.New("mail: gateway not found")
	ErrEndpointNotFound      = errors.New("mail: endpoint not found")
	ErrEndpointAuthFailed    = errors.New("mail: endpoint authentication failed")
	ErrEnvelopeDecryptFailed = errors.New("mail: envelope decryption failed")
	ErrJobPublishFailed      = errors.New("mail: job publish failed")
	ErrTenantAccessDenied    = errors.New("mail: tenant access denied")
	ErrInternal              = errors.New("mail: internal server error")
)
