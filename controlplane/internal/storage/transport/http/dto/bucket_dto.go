package storageDto

// CreateBucketRequest định nghĩa cấu trúc dữ liệu đầu vào khi tạo Bucket cá nhân.
type CreateBucketRequest struct {
	Name                 string            `json:"name" binding:"required"`
	QuotaBytes           int64             `json:"quota_bytes"`
	Policy               string            `json:"policy" binding:"required"`
	EncryptEnabled       bool              `json:"encrypt_enabled"`
	VersioningEnabled    bool              `json:"versioning_enabled"`
	ObjectLockingEnabled bool              `json:"object_locking_enabled"`
	ReplicationEnabled   bool              `json:"replication_enabled"`
	RetentionDays        int64             `json:"retention_days"`
	LegalHoldEnabled     bool              `json:"legal_hold_enabled"`
	Tags                 map[string]string `json:"tags"`
}

// CreateTenantBucketRequest định nghĩa cấu trúc dữ liệu đầu vào khi tạo Bucket doanh nghiệp.
type CreateTenantBucketRequest struct {
	Name                 string            `json:"name" binding:"required"`
	QuotaBytes           int64             `json:"quota_bytes"`
	Policy               string            `json:"policy"`
	EncryptEnabled       bool              `json:"encrypt_enabled"`
	VersioningEnabled    bool              `json:"versioning_enabled"`
	ObjectLockingEnabled bool              `json:"object_locking_enabled"`
	ReplicationEnabled   bool              `json:"replication_enabled"`
	RetentionDays        int64             `json:"retention_days"`
	LegalHoldEnabled     bool              `json:"legal_hold_enabled"`
	Tags                 map[string]string `json:"tags"`
}

// UpdateQuotaRequest định nghĩa cấu trúc dữ liệu khi thay đổi hạn mức quota.
type UpdateQuotaRequest struct {
	QuotaBytes int64 `json:"quota_bytes" binding:"required"`
}

// UpdateTenantQuotaRequest định nghĩa cấu trúc dữ liệu khi thay đổi hạn mức quota của tenant bucket.
type UpdateTenantQuotaRequest struct {
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

type UpdateBucketVersioningRequest struct {
	VersioningEnabled bool `json:"versioning_enabled"`
}

type UpdateTenantBucketVersioningRequest struct {
	VersioningEnabled bool `json:"versioning_enabled"`
}

type BucketLifecycleRuleDTO struct {
	ID                                 string `json:"id" binding:"required"`
	Enabled                            bool   `json:"enabled"`
	Prefix                             string `json:"prefix"`
	ExpirationDays                     int    `json:"expiration_days"`
	NoncurrentVersionExpirationDays    int    `json:"noncurrent_version_expiration_days"`
	AbortIncompleteMultipartUploadDays int    `json:"abort_incomplete_multipart_upload_days"`
}

type TenantBucketLifecycleRuleDTO struct {
	ID                                 string `json:"id" binding:"required"`
	Enabled                            bool   `json:"enabled"`
	Prefix                             string `json:"prefix"`
	ExpirationDays                     int    `json:"expiration_days"`
	NoncurrentVersionExpirationDays    int    `json:"noncurrent_version_expiration_days"`
	AbortIncompleteMultipartUploadDays int    `json:"abort_incomplete_multipart_upload_days"`
}

type UpdateBucketLifecycleRequest struct {
	Rules []BucketLifecycleRuleDTO `json:"rules"`
}

type UpdateTenantBucketLifecycleRequest struct {
	Rules []TenantBucketLifecycleRuleDTO `json:"rules"`
}

type BucketLifecycleResponse struct {
	Rules []BucketLifecycleRuleDTO `json:"rules"`
}

type TenantBucketLifecycleResponse struct {
	Rules []TenantBucketLifecycleRuleDTO `json:"rules"`
}
