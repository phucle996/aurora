package hierarchyTaxonomy

import "errors"

var (
	ErrNotFound           = errors.New("hierarchy: resource not found")
	ErrAlreadyExists      = errors.New("hierarchy: resource already exists")
	ErrConflict           = errors.New("hierarchy: resource conflict")
	ErrInvalidTransition  = errors.New("hierarchy: invalid state transition")
	ErrPreconditionFailed = errors.New("hierarchy: precondition failed")
)
