package coreTaxonomy

import "errors"

var (
	ErrFamilyNotFound        = errors.New("core service: secret family not found")
	ErrInvalidTTL            = errors.New("core service: invalid ttl")
	ErrInvalidVersionSet     = errors.New("core service: secret family must have between 1 and 2 versions")
	ErrNoActiveVersion       = errors.New("core service: no active version available")
	ErrNewVersionRequired    = errors.New("core service: new version required")
	ErrSecretBootstrapFamily = errors.New("core service: invalid bootstrap family")
	ErrMissingPrimaryVersion = errors.New("core service: missing primary version")
	ErrDecryptSecret         = errors.New("core service: decrypt secret failed")

	// Zone REST API errors.
	ErrZoneInvalidInput             = errors.New("core zone: invalid input")
	ErrZoneCodeAlreadyExists        = errors.New("core zone: code already exists")
	ErrZoneNotFound                 = errors.New("core zone: not found")
	ErrZoneInvalidTransition        = errors.New("core zone: invalid transition")
	ErrZoneDeletePreconditionFailed = errors.New("core zone: delete precondition failed")
	ErrZoneServiceInvalidInput      = errors.New("core zone service: invalid input")
	ErrZoneServiceZoneNotFound      = errors.New("core zone service: zone not found")
	ErrZoneServiceInvalidType       = errors.New("core zone service: invalid service type")
	ErrZoneServiceStateConflict     = errors.New("core zone service: state conflict")
	ErrZoneCongested                = errors.New("core zone: zone is congested, backpressure limit exceeded")
)
