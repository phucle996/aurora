package coreTaxonomy

import "errors"

var (

	// generic
	ErrGenUUID = errors.New("hierarchy: generate UUID failed")

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

	// Workspace REST API errors.
	ErrWorkspaceInvalidInput   = errors.New("workspace: invalid input")
	ErrWorkspaceZoneNotFound   = errors.New("workspace: zone not found or not active")
	ErrWorkspaceTenantNotFound = errors.New("workspace: tenant not found or not active")
	ErrWorkspaceInsertFailed   = errors.New("workspace: insert failed due to constraint violation")
)
