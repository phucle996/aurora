package iamReq

type RegisterRequest struct {
	Username   string  `json:"username" binding:"required,min=6"`
	Email      string  `json:"email" binding:"required,email"`
	Password   string  `json:"password" binding:"required,min=8"`
	RePassword string  `json:"re_password" binding:"required,min=8"`
	Fullname   string  `json:"fullname" binding:"required"`
	Phone      *string `json:"phone" binding:"omitempty,e164"`
	Location   *string `json:"location" binding:"omitempty"`
	Timezone   *string `json:"timezone" binding:"omitempty"`
}

type LoginRequest struct {
	Username        string `json:"username" binding:"required,min=6"`
	Password        string `json:"password" binding:"required,min=8"`
	DevicePublicKey string `json:"device_public_key" binding:"required,min=16"`
}
