package constant

// Admin header
const (
	HeaderAdminStepUpMethod = "X-Admin-StepUp-Method"
	HeaderAdminStepUpCode   = "X-Admin-StepUp-Code"

	HeaderAdminSignature = "X-Admin-Signature"
	HeaderAdminTimestamp = "X-Admin-Timestamp"
	HeaderAdminNonce     = "X-Admin-Nonce"

	HeaderRateLimitLimit     = "X-RateLimit-Limit"
	HeaderRateLimitRemaining = "X-RateLimit-Remaining"
	HeaderRateLimitReset     = "X-RateLimit-Reset"
	HeaderRetryAfter         = "Retry-After"
)
