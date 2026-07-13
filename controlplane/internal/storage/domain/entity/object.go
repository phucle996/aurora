package storageEntity

import (
	"github.com/google/uuid"
)

// [COMMENT]: SignObjectAction định nghĩa các hành động được phép trên S3 Object.
type SignObjectAction string

const (
	SignObjectActionList     SignObjectAction = "list"
	SignObjectActionUpload   SignObjectAction = "upload"
	SignObjectActionDownload SignObjectAction = "download"
	SignObjectActionDelete   SignObjectAction = "delete"
)

// [COMMENT]: RequestObjectPresignParam chứa tất cả tham số gửi lên từ Client khi yêu cầu thao tác file/folder (list/upload/download/delete).
type RequestObjectPresignParam struct {
	BucketID    uuid.UUID
	BucketName  string
	Key         string
	Action      SignObjectAction
	ContentType string
	UserID      uuid.UUID
	WorkspaceID uuid.UUID
	ZoneID      uuid.UUID
}
