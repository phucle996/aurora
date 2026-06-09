package l1_cache

// Zeroable định nghĩa interface cho các đối tượng nhạy cảm cần được xóa sạch (zero out) dữ liệu khi bị hủy/evict.
type Zeroable interface {
	Zero()
}

// L1Envelope là cấu trúc vỏ bọc cho các giá trị cache trên RAM L1 (không cần JSON tags).
// Giúp tối ưu hóa tốc độ truy xuất trên bộ nhớ thô và tránh phản xạ JSON không cần thiết.
type L1Envelope struct {
	// Key là khóa định danh duy nhất của cache item
	Key string
	// Version đại diện cho phiên bản đơn điệu của dữ liệu (thường là UnixNano timestamp khi load)
	Version int64
	// Value là dữ liệu gốc thô (struct hoặc slice) dạng interface{} được lưu trực tiếp trên RAM (Zero-Serialization)
	Value interface{}
}
