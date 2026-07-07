package coreTaxonomy

import "errors"

var (

	// generic
	ErrGenUUID = errors.New("hierarchy: generate UUID failed")

	// notfound
	ErrZoneNotFound      = errors.New("zone not found or not active")
	ErrTenantNotFound    = errors.New("tenant not found or not active")
	ErrNoRowAffected     = errors.New("no row affected")
	ErrCodeAlreadyExists = errors.New("code already exists")

	// ===========================================

	// Zone REST API errors.
	ErrZoneInvalidInput = errors.New("core zone: invalid input")

	ErrZoneInvalidTransition        = errors.New("core zone: invalid transition")
	ErrZoneDeletePreconditionFailed = errors.New("core zone: delete precondition failed")
	ErrZoneServiceInvalidInput      = errors.New("core zone service: invalid input")
	ErrZoneServiceZoneNotFound      = errors.New("core zone service: zone not found")
	ErrZoneServiceInvalidType       = errors.New("core zone service: invalid service type")
	ErrZoneServiceStateConflict     = errors.New("core zone service: state conflict")
	ErrZoneCongested                = errors.New("core zone: zone is congested, backpressure limit exceeded")

	// Workspace REST API errors.
	ErrWorkspaceInvalidInput      = errors.New("workspace: invalid input")
	ErrWorkspaceCodeAlreadyExists = errors.New("workspace: code already exists within this scope")
	ErrWorkspaceInsertFailed      = errors.New("workspace: insert failed due to constraint violation")
	// [COMMENT]: Thêm lỗi sentinel khi không tìm thấy Workspace để xử lý Get/Delete logic
	ErrWorkspaceNotFound          = errors.New("workspace: workspace not found")

	// Tenant REST API errors.

	ErrTenantInvalidInput    = errors.New("tenant: invalid input")
	ErrTenantCreationBlocked = errors.New("tenant: creation blocked under tenant context")
	ErrTenantInsertFailed    = errors.New("tenant: insert failed")
)
