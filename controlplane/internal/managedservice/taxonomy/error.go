package taxonomy

import "errors"

var (
	// Runtime aggregate errors describe behavior, not the physical personal or
	// tenant table. The workflow operation in logs/traces supplies object context.
	ErrNotFound           = errors.New("managed service: record not found")
	ErrConflict           = errors.New("managed service: concurrent modification")
	ErrPreconditionFailed = errors.New("managed service: precondition failed")
	ErrUnavailable        = errors.New("managed service: dependency unavailable")

	ErrCatalogNotFound            = errors.New("managed service: catalog record not found")
	ErrCatalogCodeConflict        = errors.New("managed service: catalog code conflict")
	ErrCatalogParentRetired       = errors.New("managed service: catalog parent retired")
	ErrCatalogInvalidTransition   = errors.New("managed service: catalog invalid transition")
	ErrCatalogConcurrentChange    = errors.New("managed service: catalog concurrent modification")
	ErrCatalogValidationFailed    = errors.New("managed service: catalog validation failed")
	ErrCatalogRevisionStale       = errors.New("managed service: catalog revision stale")
	ErrCatalogRecordPinned        = errors.New("managed service: catalog record pinned")
	ErrCatalogRecordImmutable     = errors.New("managed service: catalog record immutable")
	ErrCustomerCatalogNotFound    = errors.New("managed service: customer catalog not found")
	ErrCustomerCatalogStale       = errors.New("managed service: customer catalog stale")
	ErrCustomerCatalogUnavailable = errors.New("managed service: customer catalog unavailable")
)
