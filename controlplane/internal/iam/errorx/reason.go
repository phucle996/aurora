package iamErrorx

// Admin auth reason codes for observability.
// Keep these coarse-grained and bounded; put fine details into Cause.
const (
	ReasonAuthRegisterInvalidArgument = "auth_register_invalid_argument"
	ReasonAuthRegisterDependencyError = "auth_register_dependency_error"
	ReasonAuthRegisterUserExists      = "auth_register_user_exists"

	ReasonAuthLoginInvalidCredentials      = "auth_login_invalid_credentials"
	ReasonAuthLoginDependencyError         = "auth_login_dependency_error"
	ReasonAuthLoginVerificationRequired    = "auth_login_verification_required"
	ReasonAuthLoginVerificationIssueError  = "auth_login_verification_issue_error"
	ReasonAuthLoginVerificationPublishFail = "auth_login_verification_publish_error"
	ReasonAuthLoginAuthUnavailable         = "auth_login_auth_unavailable"
	ReasonAuthLoginTokenIssue              = "auth_login_token_issue"
	ReasonAuthLoginInvalidArgument         = "auth_login_invalid_argument"
	ReasonAuthLoginInvalidDevicePublicKey  = "auth_login_invalid_device_public_key"

	ReasonRefreshInvalidSession  = "refresh_invalid_session"
	ReasonRefreshDependencyError = "refresh_dependency_error"
	ReasonRefreshAuthUnavailable = "refresh_auth_unavailable"
	ReasonRefreshTokenIssue      = "refresh_token_issue"

	ReasonOneTimeTokenInvalidPurposeOrUser = "ott_invalid_purpose_or_user"
	ReasonOneTimeTokenInvalidOrExpired     = "ott_invalid_or_expired"
	ReasonOneTimeTokenIssueConfigError     = "ott_issue_config_error"
	ReasonOneTimeTokenIssueDependencyError = "ott_issue_dependency_error"
	ReasonOneTimeTokenConsumeDependencyErr = "ott_consume_dependency_error"

	ReasonRbacInvalidArgument    = "rbac_invalid_argument"
	ReasonRbacRoleNotFound       = "rbac_role_not_found"
	ReasonRbacPermissionNotFound = "rbac_permission_not_found"
	ReasonRbacDependencyError    = "rbac_dependency_error"

	ReasonAdminBootstrapLockError         = "admin_bootstrap_lock_error"
	ReasonAdminBootstrapPreconditionError = "admin_bootstrap_precondition_error"
	ReasonAdminBootstrapNotAllowed        = "admin_bootstrap_not_allowed"
	ReasonAdminBootstrapPersistError      = "admin_bootstrap_persist_error"
	ReasonAdminBootstrapNotifyError       = "admin_bootstrap_notify_error"
	ReasonAdminBootstrapRollbackError     = "admin_bootstrap_rollback_error"

	ReasonAdminRotationLockBusy   = "admin_rotation_lock_busy"
	ReasonAdminRotationDependency = "admin_rotation_dependency_error"
	ReasonAdminRotationDelivery   = "admin_rotation_delivery_error"
	ReasonAdminRotationTrigger    = "admin_rotation_trigger_error"

	ReasonAdminLoginInvalidArgument    = "admin_login_invalid_argument"
	ReasonAdminLoginInvalidCredential  = "admin_login_invalid_credential"
	ReasonAdminLoginMFAInvalid         = "admin_login_mfa_invalid"
	ReasonAdminLoginDependencyError    = "admin_login_dependency_error"
	ReasonAdminLoginCacheError         = "admin_login_cache_error"
	ReasonAdminLoginDeviceBindingError = "admin_login_device_binding_error"
	ReasonAdminLoginTokenIssue         = "admin_login_token_issue"
	ReasonAdminLoginAuthUnavailable    = "admin_login_auth_unavailable"

	ReasonAdminRefreshInvalidArgument = "admin_refresh_invalid_argument"
	ReasonAdminRefreshRuntimeInvalid  = "admin_refresh_runtime_invalid"
	ReasonAdminRefreshDependencyError = "admin_refresh_dependency_error"
	ReasonAdminRefreshCacheError      = "admin_refresh_cache_error"
	ReasonAdminRefreshTokenIssue      = "admin_refresh_token_issue"
	ReasonAdminRefreshAuthUnavailable = "admin_refresh_auth_unavailable"

	ReasonAdminLogoutRuntimeError = "admin_logout_runtime_error"
	ReasonAdminLogoutCacheError   = "admin_logout_cache_error"

	ReasonAdminFinalizeDependencyError = "admin_finalize_dependency_error"
)
