package mailTaxonomy

import "errors"

var (
	ErrInvalidArgument     = errors.New("mail: invalid argument")
	ErrAlreadyExists       = errors.New("mail: already exists")
	ErrWorkspaceNotFound   = errors.New("mail: workspace not found")
	ErrVersionConflict     = errors.New("mail: version conflict")
	ErrOperationInProgress = errors.New("mail: operation in progress")
	ErrConsumerNotFound    = errors.New("mail: consumer not found")
	ErrTemplateNotFound    = errors.New("mail: template not found")
	ErrTemplateInUse       = errors.New("mail: template is used by an active consumer")
	ErrTemplateSyntax      = errors.New("mail: template syntax error")
	ErrRuntimeUnavailable  = errors.New("mail: runtime watch unavailable")
	ErrInternal            = errors.New("mail: internal server error")
	// [COMMENT]: Fail-close zstd/UTF-8 — không bao giờ fallback raw bytes khi gặp các lỗi này.
	ErrHTMLDecompressFailed       = errors.New("mail: html template zstd decompression failed")
	ErrHTMLDecompressSizeExceeded = errors.New("mail: html template decompressed size exceeded limit")
	ErrHTMLUTF8Invalid            = errors.New("mail: html template contains invalid UTF-8 bytes")
)
