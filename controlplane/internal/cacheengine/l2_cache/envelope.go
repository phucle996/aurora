package l2_cache

// L2Envelope là cấu trúc vỏ bọc cho các giá trị cache trên L2 Redis hoặc truyền tải qua Pub/Sub.
// Có đầy đủ JSON tags để phục vụ quá trình serialization/deserialization.
type L2Envelope struct {
	// Key là khóa định danh duy nhất của cache item
	Key string `json:"key"`
	// Version đại diện cho phiên bản đơn điệu của dữ liệu
	Version int64 `json:"version"`
	// Value là dữ liệu gốc thô được serialize sang JSON
	Value interface{} `json:"value"`
}
