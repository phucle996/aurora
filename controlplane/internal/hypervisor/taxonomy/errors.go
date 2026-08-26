package hypervisorTaxonomy

import "errors"

var (
	ErrNotFound                             = errors.New("hypervisor: virtual machine not found")
	ErrScopeUnavailable                     = errors.New("hypervisor: workspace or zone is unavailable")
	ErrNameConflict                         = errors.New("hypervisor: virtual machine name already uses another specification")
	ErrImageNotFound                        = errors.New("hypervisor: image artifact not found")
	ErrImageConflict                        = errors.New("hypervisor: image code and revision already exist")
	ErrImageStateConflict                   = errors.New("hypervisor: image state does not allow this operation")
	ErrVMStateConflict                      = errors.New("hypervisor: virtual machine state does not allow this operation")
	ErrCommercialAdmissionDenied            = errors.New("hypervisor: commercial admission does not allow billable allocation")
	ErrPricingUnavailable                   = errors.New("hypervisor: required pricing schedules are unavailable")
	ErrInvalidCommercialAdmissionProjection = errors.New("hypervisor: invalid commercial admission projection event")
	ErrInvalidResourcePlanProjection        = errors.New("hypervisor: invalid resource plan projection event")
	ErrResourcePlanUnavailable              = errors.New("hypervisor: selected resource plan is unavailable")
	ErrResourcePlanCacheUnavailable         = errors.New("hypervisor: resource plan admission cache is unavailable")
)
