package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

// IamOutboxStatus đại diện cho trạng thái của công việc trong IAM Outbox.
type IamOutboxStatus string

const (
	// IamOutboxStatusPending: Công việc mới được tạo, chờ CDC đẩy đi
	IamOutboxStatusPending IamOutboxStatus = "PENDING"
	// IamOutboxStatusPublished: Công việc đã được chuyển tiếp sang hàng đợi thành công
	IamOutboxStatusPublished IamOutboxStatus = "PUBLISHED"
	// IamOutboxStatusProcessing: Công việc đang được thực thi ở Dataplane
	IamOutboxStatusProcessing IamOutboxStatus = "PROCESSING"
	// IamOutboxStatusCompleted: Công việc hoàn tất (phục vụ tương thích ngược)
	IamOutboxStatusCompleted IamOutboxStatus = "COMPLETED"
	// IamOutboxStatusSucceeded: Công việc hoàn tất thành công
	IamOutboxStatusSucceeded IamOutboxStatus = "SUCCEEDED"
	// IamOutboxStatusFailed: Công việc xử lý thất bại sau khi hết số lần retry
	IamOutboxStatusFailed IamOutboxStatus = "FAILED"
)

// IamOutboxRecord đại diện cho bản ghi sự kiện Outbox bất đồng bộ thuộc module IAM.
type IamOutboxRecord struct {
	ID                   int64           // Khóa chính tự tăng (BIGSERIAL)
	EventID              uuid.UUID       // Định danh sự kiện duy nhất (Idempotency Key)
	ZoneID               uuid.UUID       // Định danh zone phục vụ multi-tenancy
	JobTopic             string          // Tên topic xử lý (e.g. mail.system.verify_account)
	Payload              []byte          // Dữ liệu nhị phân serialized Protobuf
	UserID               string          // ID của user liên quan
	Status               IamOutboxStatus // Trạng thái xử lý của công việc
	CompletedAt          *time.Time      // Thời điểm hoàn tất công việc
	JobVersion           uint32          // Phiên bản logic của Job chạy ở Dataplane
	ResourceID           string          // Định danh tài nguyên đích liên quan đến sự kiện
	PayloadSchemaVersion uint32          // Phiên bản cấu trúc dữ liệu của Payload
	TraceID              *string         // Trace ID OpenTelemetry để trace luồng bất đồng bộ
	Idle                 uint32          // Thời gian tối đa cho phép xử lý job (seconds)
	ErrorCode            *string         // Mã lỗi phản hồi từ Dataplane nếu thất bại
	ErrorMessage         *string         // Chi tiết lỗi phản hồi từ Dataplane
}
