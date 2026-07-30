package taxonomy

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
	ErrZoneInvalidInput = errors.New("hierarchy zone: invalid input")

	ErrZoneInvalidTransition        = errors.New("hierarchy zone: invalid transition")
	ErrZoneDeletePreconditionFailed = errors.New("hierarchy zone: delete precondition failed")
	ErrZoneServiceInvalidInput      = errors.New("hierarchy zone service: invalid input")
	ErrZoneServiceZoneNotFound      = errors.New("hierarchy zone service: zone not found")
	ErrZoneServiceInvalidType       = errors.New("hierarchy zone service: invalid service type")
	ErrZoneServiceStateConflict     = errors.New("hierarchy zone service: state conflict")
	ErrZoneCongested                = errors.New("hierarchy zone: zone is congested, backpressure limit exceeded")

	// Zone public encryption key lifecycle errors.
	ErrZoneEncryptionKeyZoneNotFound      = errors.New("hierarchy zone encryption key: zone not found")
	ErrZoneEncryptionKeyNotFound          = errors.New("hierarchy zone encryption key: key not found")
	ErrZoneEncryptionKeyMaterialConflict  = errors.New("hierarchy zone encryption key: public key belongs to another zone")
	ErrZoneEncryptionKeyInvalidTransition = errors.New("hierarchy zone encryption key: invalid transition")

	// Workspace REST API errors.
	ErrWorkspaceInvalidInput      = errors.New("workspace: invalid input")
	ErrWorkspaceCodeAlreadyExists = errors.New("workspace: code already exists within this scope")
	ErrWorkspaceInsertFailed      = errors.New("workspace: insert failed due to constraint violation")
	// [COMMENT]: Thêm lỗi sentinel khi không tìm thấy Workspace để xử lý Get/Delete logic
	ErrWorkspaceNotFound            = errors.New("workspace: workspace not found")
	ErrWorkspaceNotEmpty            = errors.New("workspace: workspace is not empty, active resources exist")
	ErrLastWorkspaceDeletionBlocked = errors.New("workspace: cannot delete the last remaining workspace")

	// Tenant REST API errors.

	ErrTenantInvalidInput    = errors.New("tenant: invalid input")
	ErrTenantCreationBlocked = errors.New("tenant: creation blocked under tenant context")
	ErrTenantInsertFailed    = errors.New("tenant: insert failed")
)
