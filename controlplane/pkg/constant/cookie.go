package constant

// cookie naming convention:
// - public domain cookie: domain = domain of ui (api.app.com)
// - private domain cookie: domain = domain of api (api.example.com)

const (
	AccessTokenName    = "access_token"
	RefreshTokenName   = "refresh_token"
	AdminAPITokenName  = "admin_api_token"
	DeviceIDName       = "device_id"
	DeviceSecretName   = "device_secret"
	ClientDeviceIDName = "client_device_id"
)
