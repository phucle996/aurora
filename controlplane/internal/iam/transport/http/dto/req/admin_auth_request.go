package iamReq

type AdminLoginRequest struct {
	AdminAPIKey    string `json:"admin_api_key" binding:"required,min=16"`
	MFAMethod      string `json:"mfa_method" binding:"required,oneof=totp recovery_code"`
	MFACode        string `json:"mfa_code" binding:"required,min=6"`
	DevicePublicKey string `json:"device_public_key" binding:"required,min=16"`
}
