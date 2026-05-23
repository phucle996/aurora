package iamMetrics

const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
	OutcomeUnknown = "unknown"
)

const (
	RegisterOutcomeInvalidArgument = "invalid_argument"
	RegisterOutcomeExistCheckError = "exist_check_error"
	RegisterOutcomeAlreadyExists   = "already_exists"
	RegisterOutcomeHashPasswordErr = "hash_password_error"
	RegisterOutcomeIDGenerateErr   = "id_generate_error"
	RegisterOutcomeInsertError     = "insert_error"
)

const (
	RegisterCachePathMiss       = "cache_miss"
	RegisterCachePathNotChecked = "not_checked"
	RegisterCachePathFallback   = "cache_fallback"
	RegisterCachePathHitDBCheck = "cache_hit_db_check"
)

const (
	LoginOutcomeInvalidCredentials  = "invalid_credentials"
	LoginOutcomeInvalidArgument     = "invalid_argument"
	LoginOutcomeInvalidDevicePubKey = "invalid_device_public_key"
	LoginOutcomeLoadUserError       = "load_user_error"
	LoginOutcomeVerificationReq     = "verification_required"
	LoginOutcomeVerificationIssue   = "verification_issue_error"
	LoginOutcomeVerificationPublish = "verification_publish_error"
	LoginOutcomeIssueAccessError    = "issue_access_error"
	LoginOutcomeGenerateRefreshErr  = "generate_refresh_error"
	LoginOutcomePersistRefreshErr   = "persist_refresh_error"

	LoginOutcomeVerifyMailPublishAttempt   = "verify_mail_publish_attempt"
	LoginOutcomeVerifyMailPublishError     = "verify_mail_publish_error"
	LoginOutcomeVerifyMailPublishSuccess   = "verify_mail_publish_success"
	LoginOutcomeVerifyMailPublishDuplicate = "verify_mail_publish_duplicate"
)

const (
	RefreshOutcomeInvalidSession     = "invalid_session"
	RefreshOutcomeLoadSessionError   = "load_session_error"
	RefreshOutcomeLoadUserError      = "load_user_error"
	RefreshOutcomeIssueAccessError   = "issue_access_error"
	RefreshOutcomeGenerateRefreshErr = "generate_refresh_error"
	RefreshOutcomeRotateRefreshErr   = "rotate_refresh_error"
)

const (
	AdminLoginOutcomeInvalidArgument          = "invalid_argument"
	AdminLoginOutcomeInvalidDevicePublicKey   = "invalid_device_public_key"
	AdminLoginOutcomeLoadActiveKeyError       = "load_active_key_error"
	AdminLoginOutcomeInvalidCredential        = "invalid_credential"
	AdminLoginOutcomeLoadTOTPSecretError      = "load_totp_secret_error"
	AdminLoginOutcomeMFAInvalidEmptyCode      = "mfa_invalid_empty_code"
	AdminLoginOutcomeMFAInvalidEmptySecret    = "mfa_invalid_empty_secret"
	AdminLoginOutcomeMFAValidateError         = "mfa_validate_error"
	AdminLoginOutcomeMFAInvalidCodeOrTimeSkew = "mfa_invalid_code_or_time_skew"
	AdminLoginOutcomeRecoveryLockError        = "recovery_lock_error"
	AdminLoginOutcomeConsumeRecoveryError     = "consume_recovery_error"
	AdminLoginOutcomeMFAInvalid               = "mfa_invalid"
	AdminLoginOutcomeDeviceSecretIssueError   = "device_secret_issue_error"
	AdminLoginOutcomeRuntimeCacheError        = "runtime_cache_error"
	AdminLoginOutcomeUpsertDeviceBindingErr   = "upsert_device_binding_error"
	AdminLoginOutcomeAuthUnavailable          = "auth_unavailable"
	AdminLoginOutcomeJTIIssueError            = "jti_issue_error"
	AdminLoginOutcomeRuntimeUpdateError       = "runtime_update_error"
	AdminLoginOutcomeSignTokenError           = "sign_token_error"
)

const (
	AdminRefreshOutcomeInvalidArgument = "invalid_argument"
	AdminRefreshOutcomeLoadRuntimeErr  = "load_runtime_error"
	AdminRefreshOutcomeRuntimeNotFound = "runtime_not_found"
	AdminRefreshOutcomeTouchRuntimeErr = "touch_runtime_error"
	AdminRefreshOutcomeRuntimeConflict = "runtime_conflict"
	AdminRefreshOutcomeAuthUnavailable = "auth_unavailable"
	AdminRefreshOutcomeJTIIssueError   = "jti_issue_error"
	AdminRefreshOutcomeSignTokenError  = "sign_token_error"
)

const (
	AdminRotationOutcomeLockContention = "lock_contention"
	AdminRotationOutcomeDeliveryFail   = "delivery_fail"
	AdminRotationOutcomeRotateFail     = "rotate_fail"
)
