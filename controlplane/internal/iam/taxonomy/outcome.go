package iamTaxonomy

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
	// RegisterOutcomeArgon2HashFailed đánh dấu lỗi băm mật khẩu Argon2id ngốn nhiều CPU.
	RegisterOutcomeArgon2HashFailed = "argon2_hash_failed"
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
	SessionOutcomeUnauthorized = "unauthorized"
)

const (
	AdminLoginOutcomeInvalidArgument        = "invalid_argument"
	AdminLoginOutcomeInvalidDevicePublicKey = "invalid_device_public_key"
	AdminLoginOutcomeInvalidCredential      = "invalid_credential"
	AdminLoginOutcomeSystemError            = "system_error"
)

const (
	AdminRefreshOutcomeInvalidArgument = "invalid_argument"
	AdminRefreshOutcomeInvalidSession  = "invalid_session"
	AdminRefreshOutcomeSystemError     = "system_error"
)

const (
	AdminRotateLockBusy     = "lock_contention"
	AdminRotateDeliveryFail = "delivery_fail"
	AdminRotateFail         = "rotate_fail"
)

const (
	AdminLogoutOutcomeInvalidArgument = "invalid_argument"
	AdminLogoutOutcomeSystemError     = "system_error"
)

const (
	AdminFinalizeOutcomeSystemError = "finalize_dependency_error"
)

const (
	RbacOutcomeDependencyError = "rbac_dependency_error"
	RbacOutcomeCacheError      = "rbac_cache_error"
)
