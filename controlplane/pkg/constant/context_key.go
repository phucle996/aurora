package constant

// gin context key
// this is source of truth for gin context key
// / the middleware set the context key when the request is authenticated
// / the handler can get the value from the context
const (
	ContextKeyAdminDeviceID     = "admin.device_id"
	ContextKeyAdminDeviceSecret = "admin.device_secret"
	ContextKeyAdminTokenJTI     = "admin.token_jti"
)
