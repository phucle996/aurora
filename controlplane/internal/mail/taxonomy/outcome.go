package mailTaxonomy

const (
	OutcomeSuccess            = "success"
	OutcomeFailure            = "failure"
	OutcomeUnknown            = "unknown"
	OutcomeInvalidArgument    = "invalid_argument"
	OutcomeNotFound           = "not_found"
	OutcomeCryptoError        = "crypto_error"
	OutcomeDatabaseError      = "database_error"
	OutcomeSerializationError = "serialization_error"
	OutcomeTimeout            = "timeout"
	OutcomeInternalError      = "internal_error"
)

const (
	RenderSuccess      = "render_success"
	RenderTemplateFail = "render_template_fail"

	RouteGatewayMatch = "gateway_match"
	RouteGatewayMiss  = "gateway_miss"

	RedisJobEnqueued = "job_enqueued"
	RedisJobFailed   = "job_enqueue_failed"

	EndpointTLSFailed = "endpoint_tls_failed"
)
