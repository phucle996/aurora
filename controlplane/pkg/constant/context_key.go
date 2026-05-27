package constant

// gin context key
// this is source of truth for gin context key
// / the middleware set the context key when the request is authenticated
// / the handler can get the value from the context
const (
	ContextKeyAdminAccessKey    = "admin.access_key"
	ContextKeyAdminAccessSecret = "admin.access_secret"
	ContextKeyAdminTokenJTI     = "admin.token_jti"

	ContextKeyJWTClaims       = "jwt_claims"
	ContextKeyUserID          = "user_id"
	ContextKeyRole            = "role"
	ContextKeyJTI             = "jti"
	ContextKeyLevel           = "level"
	ContextKeyTenant          = "tenant"
	ContextKeyRuntimeAccessKey = "runtime_access_key"
	ContextKeyTrackedDeviceID = "tracked_device_id"
	ContextKeyTrackingID      = "tracking_id"

	ContextKeyHeaderDeviceHostname = "header.device_hostname"
	ContextKeyHeaderDeviceName     = "header.device_name"
	ContextKeyHeaderClientDeviceID = "header.client_device_id"
)
