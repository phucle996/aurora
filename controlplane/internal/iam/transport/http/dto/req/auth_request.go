package iamReq

type RegisterRequest struct {
	Username string  `json:"username" binding:"required,min=6"`
	Email    string  `json:"email" binding:"required,email"`
	Password string  `json:"password" binding:"required,min=8"`
	Fullname string  `json:"fullname" binding:"required"`
	Phone    *string `json:"phone" binding:"omitempty,e164"`
	Location *string `json:"location" binding:"omitempty"`
	Timezone *string `json:"timezone" binding:"omitempty"`
}

type VerifyAccountRequest struct {
	UserID  string `json:"user_id" binding:"required,uuid"`
	EventID string `json:"event_id" binding:"required,uuid"`
	Token   string `json:"token" binding:"required,min=32,max=256"`
}

type LoginRequest struct {
	Username        string `json:"username" binding:"required"`
	Password        string `json:"password" binding:"required"`
	DevicePublicKey string `json:"device_public_key" binding:"required,min=16"`
	TrustDevice     bool   `json:"trust_device"`
	ZoneCode        string `json:"zone_code" binding:"required"`
}
