package storageTaxonomy

import "errors"

// [COMMENT]: Khai báo các lỗi logic nghiệp vụ đặc thù của phân hệ Storage.
var (
	// [COMMENT]: Không tìm thấy Bucket yêu cầu trong hệ thống.
	ErrBucketNotFound = errors.New("storage: bucket not found")
	
	// [COMMENT]: Tên Bucket vật lý đã tồn tại trên phân vùng hạ tầng.
	ErrBucketAlreadyExists = errors.New("storage: bucket name already exists")
	
	// [COMMENT]: Vượt quá hạn mức dung lượng cho phép của Bucket.
	ErrQuotaExceeded = errors.New("storage: bucket capacity quota exceeded")
	
	// [COMMENT]: Tên Bucket không đúng định dạng chuẩn S3/MinIO (ký tự thường, số, gạch ngang).
	ErrInvalidBucketName = errors.New("storage: invalid bucket name format")
	
	// [COMMENT]: Không tìm thấy cặp Access Key / Credentials tương ứng.
	ErrCredentialNotFound = errors.New("storage: credential not found")
)
