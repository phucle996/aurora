package storageDto

// [COMMENT]: RequestObjectPresignRequest định nghĩa body nhận vào khi yêu cầu xin cấp link hoặc duyệt đối tượng (list/upload/download/delete).
type RequestObjectPresignRequest struct {
	Action      string `json:"action" binding:"required,oneof=list upload download delete"`
	BucketName  string `json:"bucket_name" binding:"required"`
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
}
