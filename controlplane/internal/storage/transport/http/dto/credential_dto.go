package storageDto

// [COMMENT]: CreateCredentialRequest đại diện cho payload yêu cầu tạo Access Key mới.
type CreateCredentialRequest struct {
	Policy string `json:"policy" binding:"required"`
}

// [COMMENT]: CredentialResponse đại diện cho dữ liệu trả về cho client.
type CredentialResponse struct {
	ID        string `json:"id"`
	BucketID  string `json:"bucket_id"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key,omitempty"` // Chỉ trả về khi tạo mới (raw key)
	Policy    string `json:"policy"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
