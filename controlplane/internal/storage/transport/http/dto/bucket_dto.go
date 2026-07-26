package storageDto

// [COMMENT]: CreateBucketRequest định nghĩa cấu trúc dữ liệu đầu vào khi tạo Bucket mới.
type CreateBucketRequest struct {
	Name                 string            `json:"name" binding:"required"`
	QuotaBytes           int64             `json:"quota_bytes"`
	Policy               string            `json:"policy" binding:"required"`
	EncryptEnabled       *bool             `json:"encrypt_enabled" binding:"required"`
	VersioningEnabled    *bool             `json:"versioning_enabled" binding:"required"`
	ObjectLockingEnabled *bool             `json:"object_locking_enabled" binding:"required"`
	ReplicationEnabled   *bool             `json:"replication_enabled" binding:"required"`
	RetentionDays        int64             `json:"retention_days"`
	LegalHoldEnabled     *bool             `json:"legal_hold_enabled" binding:"required"`
	Tags                 map[string]string `json:"tags"`
}

// [COMMENT]: UpdateQuotaRequest định nghĩa cấu trúc dữ liệu khi thay đổi hạn mức quota.
type UpdateQuotaRequest struct {
	QuotaBytes int64 `json:"quota_bytes" binding:"required"`
}

// RequestStorageAccessRequest requests an ephemeral access session rather
// than returning client credentials. The gateway consumes the session only
// after ACR has authenticated the Trinity cookie.
type RequestStorageAccessRequest struct {
	DurationSeconds int64    `json:"duration_seconds" binding:"required,min=60,max=3600"`
	Actions         []string `json:"actions"`
	KeyPrefix       string   `json:"key_prefix"`
}
