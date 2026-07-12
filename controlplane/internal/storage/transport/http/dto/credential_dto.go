package storageDto

// [COMMENT]: CreateCredentialRequest đại diện cho payload yêu cầu tạo Access Key mới.
type CreateCredentialRequest struct {
	Policy string `json:"policy" binding:"required"`
}
