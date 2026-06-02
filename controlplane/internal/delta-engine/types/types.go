package types

import "time"

// DeltaOp đại diện cho loại hành động thay đổi trạng thái.
type DeltaOp string

const (
	OpCreate DeltaOp = "CREATE"
	OpUpdate DeltaOp = "UPDATE"
	OpDelete DeltaOp = "DELETE"
)

// DeltaEvent là mô hình sự kiện thay đổi trạng thái được truyền đi qua NATS và Outbox.
type DeltaEvent struct {
	ID        string    `json:"id"`
	Entity    string    `json:"entity"` // Ví dụ: "zone", "rate_policy"
	Op        DeltaOp   `json:"op"`
	Payload   []byte    `json:"payload"`
	Version   uint64    `json:"version"`
	Timestamp time.Time `json:"timestamp"`
}

// ZoneState biểu diễn trạng thái của một Zone trong RAM.
type ZoneState struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RatePolicyState biểu diễn trạng thái của Rate Policy trong RAM.
type RatePolicyState struct {
	ID        string    `json:"id"`
	ZoneID    string    `json:"zone_id"`
	Limit     int64     `json:"limit"`
	Burst     int64     `json:"burst"`
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}
