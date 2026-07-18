package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

// IamOutboxStatus đại diện cho trạng thái của công việc trong IAM Outbox.
type IamOutboxStatus string

const (
	// IamOutboxStatusPending: Trạng thái ban đầu khi sự kiện IAM được ghi nhận vào Outbox Table.
	IamOutboxStatusPending IamOutboxStatus = "PENDING"

	// [COMMENT]: Dispatcher lease states tách delivery scheduling khỏi executor processing state.
	IamOutboxStatusPublishing IamOutboxStatus = "PUBLISHING"
	IamOutboxStatusPublished  IamOutboxStatus = "PUBLISHED"

	// IamOutboxStatusProcessing: Trạng thái khi Dataplane bắt đầu tiêu thụ và thực thi sự kiện IAM.
	IamOutboxStatusProcessing IamOutboxStatus = "PROCESSING"

	// IamOutboxStatusSucceeded: Sự kiện IAM đã được thực thi thành công hoàn tất (Terminal State).
	IamOutboxStatusSucceeded IamOutboxStatus = "SUCCEEDED"

	// IamOutboxStatusFailed: Sự kiện IAM thất bại do lỗi xử lý hoặc hết số lần thử lại (Terminal State).
	IamOutboxStatusFailed IamOutboxStatus = "FAILED"
)

// IamOutboxRecord đại diện cho bản ghi sự kiện Outbox bất đồng bộ thuộc module IAM.
type IamOutboxRecord struct {
	ID                   int64           // Khóa chính tự tăng (BIGSERIAL)
	EventID              uuid.UUID       // Định danh sự kiện duy nhất (Idempotency Key)
	RoutingScope         string          // Phạm vi định tuyến và thực thi (e.g. platform, zone:vn)
	JobTopic             string          // Tên topic xử lý (e.g. mail.system.verify_account)
	Payload              []byte          // Dữ liệu nhị phân serialized Protobuf
	OwnerID              uuid.UUID       // ID ví chịu tác động; không được suy diễn từ actor
	OwnerType            string          // PERSONAL | TENANT
	ActorUserID          uuid.UUID       // User khởi tạo thao tác, dùng cho audit/notification
	Status               IamOutboxStatus // Trạng thái xử lý của công việc
	CompletedAt          *time.Time      // Thời điểm hoàn tất công việc
	JobVersion           uint32          // Phiên bản logic của Job chạy ở Dataplane
	ResourceID           string          // Định danh tài nguyên đích liên quan đến sự kiện
	PayloadSchemaVersion uint32          // Phiên bản cấu trúc dữ liệu của Payload
	// TraceID: Mã định danh phân tán (OpenTelemetry Trace ID) dùng để liên kết chuỗi vết hoạt động (Distributed Tracing)
	// Lưu trữ dạng nhị phân BYTEA (16 bytes) thay vì string hex 32 ký tự để tối ưu hóa bộ nhớ
	TraceID      []byte
	Idle         uint32  // Thời gian tối đa cho phép xử lý job (seconds)
	ErrorCode    *string // Mã lỗi phản hồi từ Dataplane nếu thất bại
	ErrorMessage *string // Chi tiết lỗi phản hồi từ Dataplane
}
