package hypervisorTaxonomy

import "errors"

var (
	ErrNotFound           = errors.New("hypervisor: virtual machine not found")
	ErrScopeUnavailable   = errors.New("hypervisor: workspace or zone is unavailable")
	ErrNameConflict       = errors.New("hypervisor: virtual machine name already uses another specification")
	ErrProvisioningFailed = errors.New("hypervisor: virtual machine provisioning has failed")
)
