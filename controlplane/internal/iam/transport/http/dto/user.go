package iamDto

// UpdateMyProfileRequest is intentionally an allow-list. Identifier and
// credential fields are absent and strict JSON decoding rejects them upstream.
type UpdateMyProfileRequest struct {
	Fullname  string `json:"fullname"`
	Phone     string `json:"phone"`
	Address   string `json:"address"`
	AvatarURL string `json:"avatar_url"`
	Bio       string `json:"bio"`
	Locale    string `json:"locale"`
	Timezone  string `json:"timezone"`
}

type ResetUserPasswordRequest struct {
	Password string `json:"password"`
}
