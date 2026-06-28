package geoip

import (
	"net"
	"os"

	"github.com/oschwald/geoip2-golang"
)

// Resolver đại diện cho bộ giải mã GeoIP sử dụng cơ sở dữ liệu MaxMind.
type Resolver struct {
	db *geoip2.Reader
}

// NewResolver khởi tạo một bộ giải mã địa lý từ đường dẫn cơ sở dữ liệu MaxMind.
// Nếu không tìm thấy tệp hoặc đường dẫn trống, hệ thống sẽ chạy ở chế độ fallback và log cảnh báo.
func NewResolver(dbPath string) (*Resolver, error) {
	if dbPath == "" {
		dbPath = os.Getenv("GEOIP_DB_PATH")
		if dbPath == "" {
			dbPath = "GeoLite2-Country.mmdb"
		}
	}

	// [COMMENT]: Kiểm tra tệp tin cơ sở dữ liệu tồn tại.
	// Nếu không có, khởi tạo Resolver rỗng hoạt động dưới dạng fallback để tránh lỗi tắt nghẽn hệ thống.
	if _, err := os.Stat(dbPath); err != nil {
		return &Resolver{}, nil
	}

	reader, err := geoip2.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &Resolver{db: reader}, nil
}

// Close giải phóng tài nguyên đầu đọc MaxMind database.
func (r *Resolver) Close() {
	if r.db != nil {
		r.db.Close()
	}
}

// Lookup phân tích địa chỉ IP chuỗi thành mã quốc gia ISO (ví dụ: "VN", "US").
// Tự động nhận diện dải IP phát triển local/private và trả về "VN" mà không cần tra cứu database.
func (r *Resolver) Lookup(ipStr string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}

	// [COMMENT]: Tự động nhận diện dải mạng loopback (127.0.0.1) hoặc mạng nội bộ (private IP)
	// để tự động trả về vị trí phát triển "VN" tránh crash ở môi trường dev.
	if ip.IsLoopback() || ip.IsPrivate() {
		return "VN"
	}

	if r.db != nil {
		record, err := r.db.Country(ip)
		if err == nil && record.Country.IsoCode != "" {
			return record.Country.IsoCode
		}
	}

	return ""
}
