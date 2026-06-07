package cacheengine

// CacheEnvelope là cấu trúc vỏ bọc generic cho mọi giá trị cache trên RAM L1,
// chứa thông tin phiên bản để phục vụ đồng bộ hóa và so sánh đơn điệu (monotonic comparison).
type CacheEnvelope struct {
	// Key là khóa định danh duy nhất của cache item
	Key string
	// Version đại diện cho phiên bản đơn điệu của dữ liệu (thường là UnixNano timestamp khi load)
	Version int64
	// Value là dữ liệu gốc thô (struct hoặc slice) dạng interface{} được lưu trực tiếp trên RAM (Zero-Serialization)
	Value interface{}
}
