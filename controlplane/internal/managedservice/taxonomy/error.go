package taxonomy

import "errors"

var (
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
