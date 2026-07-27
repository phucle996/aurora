package hypervisorTaxonomy

import "errors"

var (
	ErrNotFound           = errors.New("hypervisor: virtual machine not found")
	ErrScopeUnavailable   = errors.New("hypervisor: workspace or zone is unavailable")
	ErrNameConflict       = errors.New("hypervisor: virtual machine name already uses another specification")
	ErrImageNotFound      = errors.New("hypervisor: image artifact not found")
	ErrImageConflict      = errors.New("hypervisor: image code and revision already exist")
	ErrImageStateConflict = errors.New("hypervisor: image state does not allow this operation")
)
