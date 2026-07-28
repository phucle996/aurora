package iamDto

type MFACodeRequest struct {
	Code string `json:"code" binding:"required"`
}
