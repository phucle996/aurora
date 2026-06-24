package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// 🛡️ XSSI RESPONSE WRITER WRAPPER (BỘ GHI ĐÈ PHẢN HỒI NGĂN CHẶN XSSI)
// ============================================================================

// xssiResponseWriter là một struct bao bọc (wrapper) xung quanh gin.ResponseWriter mặc định.
// Nhiệm vụ của nó là chặn dữ liệu trước khi được gửi xuống socket kết nối
// để chèn thêm tiền tố bảo mật (XSSI prefix) vào trước các nội dung JSON.
type xssiResponseWriter struct {
	gin.ResponseWriter      // Kế thừa các phương thức ghi phản hồi tiêu chuẩn của Gin
	wrotePrefix        bool // Đánh dấu đã ghi tiền tố XSSI hay chưa (tránh ghi đè trùng lặp)
}

// Write đánh chặn phương thức ghi dữ liệu byte thô của HTTP Response.
// Nếu phản hồi có Header Content-Type chứa "application/json" và tiền tố bảo mật chưa được ghi,
// nó sẽ ghi tiền tố ")]}',\n" trước, sau đó mới ghi phần dữ liệu JSON thực sự của endpoint.
func (w *xssiResponseWriter) Write(b []byte) (int, error) {
	// Lấy Content-Type hiện tại của Header phản hồi:
	contentType := w.Header().Get("Content-Type")
	
	// Điều kiện kích hoạt: 
	// 1. Chưa ghi tiền tố lần nào trong phiên làm việc của request hiện tại.
	// 2. Định dạng nội dung là ứng dụng JSON (application/json) nơi chứa dữ liệu nhạy cảm có nguy cơ bị XSSI.
	if !w.wrotePrefix && strings.Contains(contentType, "application/json") {
		w.wrotePrefix = true
		// Ghi tiền tố chống XSSI chuẩn: ")]}',\n". 
		// Tiền tố này khiến JS Parser trên trình duyệt ném ra lỗi cú pháp (Syntax Error) 
		// ngay lập tức nếu kẻ tấn công cố tình nhúng API qua thẻ <script src="...">.
		_, _ = w.ResponseWriter.Write([]byte(")]}',\n"))
	}
	
	// Tiếp tục ghi dữ liệu gốc từ API handler truyền xuống:
	return w.ResponseWriter.Write(b)
}

// WriteString là hàm tiện ích bổ trợ để ghi dữ liệu dạng chuỗi (string) thay vì byte slice.
// Hàm này gọi trực tiếp qua hàm Write(b []byte) phía trên để đảm bảo logic chèn tiền tố được đồng bộ.
func (w *xssiResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// ============================================================================
// 🛡️ MIDDLEWARE: ADMIN XSSI PROTECTION (CHỐNG TẤN CÔNG XSSI CHO ADMIN)
// ============================================================================

// AdminXSSI trả về một Gin Middleware để tự động chèn tiền tố bảo mật JSON ")]}',\n"
// cho mọi phản hồi JSON đi ra ngoài hệ thống từ phân khúc Admin.
//
// 🎯 CƠ CHẾ BẢO MẬT (XSSI - Cross-Site Script Inclusion):
//   - XSSI là lỗ hổng bảo mật cho phép trang web độc hại từ bên ngoài đọc được dữ liệu nhạy cảm
//     của người dùng bằng cách nhúng các URL JSON API của chúng ta vào thẻ <script src="...">.
//   - Trình duyệt sẽ thực thi đoạn script đó và kẻ tấn công có thể chiếm đoạt dữ liệu qua Global Scope JS.
//   - Bằng cách đặt tiền tố ")]}',\n" vào đầu phản hồi, trình duyệt sẽ coi đây là lỗi cú pháp JS vô nghĩa
//     và dừng thực thi ngay lập tức, vô hiệu hóa hoàn toàn cuộc tấn công nhúng mã độc này.
//   - Phía Client của hệ thống (Cloud UI) sẽ thực hiện bóc tách (stripping) tiền tố ")]}'," trước khi
//     parse JSON thành Object hữu dụng.
func AdminXSSI() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Thay thế Writer mặc định của Gin bằng xssiResponseWriter đã được bao bọc logic bảo mật:
		w := &xssiResponseWriter{ResponseWriter: c.Writer}
		c.Writer = w
		
		// Tiếp tục chuỗi middleware và handler xử lý tiếp theo trong pipeline:
		c.Next()
	}
}
