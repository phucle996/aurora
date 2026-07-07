package storageDto

// [COMMENT]: CreateBucketRequest định nghĩa cấu trúc dữ liệu đầu vào khi tạo Bucket mới.
type CreateBucketRequest struct {
	Name       string `json:"name" binding:"required"`
	QuotaBytes int64  `json:"quota_bytes"`
}

// [COMMENT]: UpdateQuotaRequest định nghĩa cấu trúc dữ liệu khi thay đổi hạn mức quota.
type UpdateQuotaRequest struct {
	QuotaBytes int64 `json:"quota_bytes" binding:"required"`
}

// [COMMENT]: CreateBucketResponse trả về thông tin bucket và credential vừa được tạo.
// secret_key CHỈ được trả về duy nhất 1 lần tại thời điểm tạo bucket — không thể lấy lại sau này.
type CreateBucketResponse struct {
	BucketID     string `json:"bucket_id"`
	BucketName   string `json:"bucket_name"`
	CredentialID string `json:"credential_id"`
	AccessKey    string `json:"access_key"`
	SecretKey    string `json:"secret_key"` // ⚠ Chỉ hiển thị 1 lần duy nhất, vui lòng lưu lại ngay
	Policy       string `json:"policy"`
}
