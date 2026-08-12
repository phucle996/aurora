package storageTaxonomy

import "errors"

// [COMMENT]: Khai báo các lỗi logic nghiệp vụ đặc thù của phân hệ Storage.
var (
	// [COMMENT]: Không tìm thấy tài nguyên yêu cầu trong hệ thống.
	ErrNotFound = errors.New("storage: resource not found")

	// [COMMENT]: Tài nguyên đã tồn tại.
	ErrAlreadyExists = errors.New("storage: resource already exists")

	// [COMMENT]: Vượt quá hạn mức dung lượng cho phép của Bucket.
	ErrQuotaExceeded = errors.New("storage: bucket capacity quota exceeded")

	// [COMMENT]: Tên Bucket không đúng định dạng chuẩn S3/MinIO (ký tự thường, số, gạch ngang).
	ErrInvalidBucketName = errors.New("storage: invalid bucket name format")

	// [COMMENT]: Không tìm thấy cặp Access Key / Credentials tương ứng.
	ErrCredentialNotFound = errors.New("storage: credential not found")

	// [COMMENT]: JSON Policy không hợp lệ hoặc vi phạm ranh giới bucket chỉ định.
	ErrInvalidPolicy = errors.New("storage: invalid JSON policy format or target resource")

	// [COMMENT]: Dung lượng resize mới quá nhỏ so với used_bytes hiện tại (cần trống ít nhất 1GB).
	ErrResizeLimitTooLow = errors.New("storage: requested quota must leave at least 1GB of free space above current usage")

	ErrWalletAdmissionDenied = errors.New("storage: wallet admission denied or projection is stale")
)
