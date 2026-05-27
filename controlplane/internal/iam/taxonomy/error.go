package iamTaxonomy

import "errors"

var (
	ErrInvalidArgument           = errors.New("iam: invalid argument")
	ErrInvalidEmail              = errors.New("iam: invalid email")
	ErrInvalidUsername           = errors.New("iam: invalid username")
	ErrPasswordMismatch          = errors.New("iam: password mismatch")
	ErrWeakPassword              = errors.New("iam: weak password")
	ErrUserAlreadyExist          = errors.New("iam: user already exists")
	ErrInvalidCredentials        = errors.New("iam: invalid credentials")
	ErrVerificationRequired      = errors.New("iam: verification required")
	ErrAuthenticationUnavailable = errors.New("iam: authentication unavailable")
	ErrInvalidSession            = errors.New("iam: invalid session")
	ErrUserDeviceRuntimeInvalid  = errors.New("iam cache: invalid user device runtime")

	// One-time token flow errors.
	ErrOneTimeTokenIssueFailed          = errors.New("iam: one-time token issue failed")
	ErrOneTimeTokenInvalidOrExpired     = errors.New("iam: one-time token invalid or expired")
	ErrOneTimeTokenConsumeFailed        = errors.New("iam: one-time token consume failed")
	ErrOneTimeTokenInvalidPurposeOrUser = errors.New("iam: one-time token invalid purpose or user")
	ErrOneTimeTokenCacheUnavailable     = errors.New("iam cache: one-time token cache unavailable")

	ErrAdminBootstrapNotAllowed         = errors.New("iam: admin bootstrap not allowed")
	ErrAdminBootstrapPreconditionFailed = errors.New("iam: admin bootstrap precondition failed")
	ErrAdminBootstrapPersistFailed      = errors.New("iam: admin bootstrap persist failed")
	ErrAdminBootstrapAuditFailed        = errors.New("iam: admin bootstrap audit failed")
	ErrAdminBootstrapNotifyFailed       = errors.New("iam: admin bootstrap notify failed")
	ErrAdminBootstrapLockFailed         = errors.New("iam: admin bootstrap lock failed")
	ErrAdminBootstrapLockLost           = errors.New("iam: admin bootstrap lock lost")
	ErrAdminBootstrapRollbackFailed     = errors.New("iam: admin bootstrap rollback failed")

	ErrAdminLoginInvalidCredential   = errors.New("iam: admin login invalid credential")
	ErrAdminLoginMFAInvalid          = errors.New("iam: admin login mfa invalid")
	ErrAdminLoginDeviceBindingFailed = errors.New("iam: admin login device binding failed")
	ErrAdminLoginTokenIssueFailed    = errors.New("iam: admin login token issue failed")
	ErrAdminCriticalSignatureInvalid = errors.New("iam: admin critical signature invalid")
	ErrAdminCriticalStepUpInvalid    = errors.New("iam: admin critical step-up invalid")

	ErrAdminRotationLockBusy = errors.New("iam: admin rotation lock busy")
	ErrAdminRotationFailed   = errors.New("iam: admin rotation failed")
	ErrAdminRotationDelivery = errors.New("iam: admin rotation delivery failed")
)

var (
	ErrRoleNotFound       = errors.New("iam: role not found")
	ErrPermissionNotFound = errors.New("iam: permission not found")
)
