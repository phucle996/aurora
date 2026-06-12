package iamTaxonomy

// Core Taxonomy Outcomes for IAM Service and Metrics
const (
	Success             = "success"
	Failure             = "failure"
	FailureUnknown      = "failure_unknown"
	InvalidArgument     = "invalid_argument"
	InvalidCredential   = "invalid_credential"
	InvalidSession      = "invalid_session"
	PreConditionFailed  = "precondition_failed"
	PreConditionSuccess = "precondition_success"
	LockBusy            = "lock_contention"
	LockUnknownError    = "lock_unknown_error"
	LockRelease         = "lock_release"

	TokenGenerateFail    = "token_generate_fail"
	TokenGenerateSuccess = "token_generate_success"
	TelegramSendFail     = "telegram_send_fail"
	UuidGenerateFail     = "uuid_generate_fail"
	SetAccessSessionFail = "set_access_session_fail"
	ZoneUnavailable      = "zone_unavailable"
	GetL1CacheFail       = "get_l1_cache_failed"
	GetL2CacheFail       = "get_l2_cache_failed"
)
