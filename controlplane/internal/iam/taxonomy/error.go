package iamTaxonomy

import "errors"

// Generic errors for workflow results and core logic
var (
	ErrInternalError         = errors.New("iam: internal error")
	ErrLockAlreadyHeld       = errors.New("iam: lock already held")
	ErrZeroRowsAffected      = errors.New("iam: zero rows affected")
	ErrNotFound              = errors.New("iam: not found")
	ErrPreconditionFailed    = errors.New("iam: precondition failed")
	ErrActionNotAllowed      = errors.New("iam: action not allowed")
	ErrGenTOTPFailed         = errors.New("iam: generate totp failed")
	ErrEncryptSecretFailed   = errors.New("iam: encrypt secret failed")
	ErrGenRecoveryCodeFailed = errors.New("iam: generate recovery code failed")
	ErrInvalidCredential     = errors.New("iam: invalid credential")
	ErrRecoveryCodeInvalid   = errors.New("iam: recovery code invalid or consumed")
	ErrTokenIssueFailed      = errors.New("iam: token issue failed")
	ErrUuidGenerateFailed    = errors.New("iam: uuid generate failed")

	ErrInvalidArgument           = errors.New("iam: invalid argument")
	ErrUserAlreadyExist          = errors.New("iam: user already exists")
	ErrUserNotFound              = errors.New("iam: user not found")
	ErrInvalidCredentials        = errors.New("iam: invalid credentials")
	ErrVerificationRequired      = errors.New("iam: verification required")
	ErrAuthenticationUnavailable = errors.New("iam: authentication unavailable")
	ErrInvalidSession            = errors.New("iam: invalid session")

	ErrPermissionNotFound = errors.New("iam: permission not found")
	ErrRoleRequired       = errors.New("iam: role required")
)

// token
var (
	ErrTokenExpired = errors.New("iam: token expired")
	ErrTokenConsume = errors.New("iam: token consume failed")
)
