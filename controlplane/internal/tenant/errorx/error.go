package tenantErrorx

import "errors"

var (
	ErrInvalidArgument = errors.New("tenant: invalid argument")
	ErrConflict        = errors.New("tenant: conflict")
	ErrForbidden       = errors.New("tenant: forbidden")
	ErrNotFound        = errors.New("tenant: not found")
	ErrUnavailable     = errors.New("tenant: unavailable")
)
