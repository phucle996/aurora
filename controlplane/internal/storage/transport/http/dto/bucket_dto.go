package storageDto

// [COMMENT]: CreateBucketRequest định nghĩa cấu trúc dữ liệu đầu vào khi tạo Bucket mới.
type CreateBucketRequest struct {
	Name       string `json:"name" binding:"required"`
	QuotaBytes int64  `json:"quota_bytes"`
	Policy     string `json:"policy" binding:"required"`
}

// [COMMENT]: UpdateQuotaRequest định nghĩa cấu trúc dữ liệu khi thay đổi hạn mức quota.
type UpdateQuotaRequest struct {
	QuotaBytes int64 `json:"quota_bytes" binding:"required"`
}

// [COMMENT]: RequestBucketStsRequest định nghĩa cấu trúc dữ liệu khi yêu cầu STS token.
type RequestBucketStsRequest struct {
	DurationSeconds int64 `json:"duration_seconds" binding:"required,min=900,max=3600"`
}
