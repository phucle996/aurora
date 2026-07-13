package storageDto

// [COMMENT]: CreateCredentialRequest đại diện cho payload yêu cầu tạo Access Key mới.
type CreateCredentialRequest struct {
	Policy string `json:"policy" binding:"required"`
}

// [COMMENT]: DeleteCredentialRequest đại diện cho payload yêu cầu xóa Access Key.
// access_key cần được truyền từ FE (đã có sẵn từ List response) để tránh DB lookup thêm.
type DeleteCredentialRequest struct {
	AccessKey string `json:"access_key" binding:"required"`
}
