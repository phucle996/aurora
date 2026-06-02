// ==============================================================================================
// 📂 MODULE: controlplane/internal/http/middleware/origin_csrf.go
//            Tầng Bảo Vệ CSRF Dựa Trên Cookie Authentication (Cookie-Origin CSRF Guard)
// ==============================================================================================
//
// 🎯 MỤC TIÊU:
//   Ngăn chặn tấn công CSRF (Cross-Site Request Forgery) cho các endpoint sử dụng
//   Cookie làm cơ chế xác thực. Tấn công CSRF lợi dụng việc browser tự động gửi kèm
//   cookie trong mọi request đến domain đích, ngay cả khi request được khởi tạo từ
//   một trang web độc hại của bên thứ ba.
//
// 🧩 VỊ TRÍ TRONG HỆ THỐNG:
//   - Chạy SAU Envoy Edge Gateway (Envoy đã xử lý CORS - chính sách origin ở tầng network).
//   - Đây là tầng bảo vệ thứ 2 (Defense-in-depth layer 2) với ngữ nghĩa khác hoàn toàn:
//     * Envoy CORS  → "Origin nào được phép gọi API?" (network-level policy)
//     * CookieOriginGuard → "Request này có bị kẻ tấn công giả mạo không?" (app-level CSRF)
//
// 🔒 NGUYÊN LÝ CSRF VÀ TẠI SAO CHỈ CẦN GUARD CHO COOKIE-AUTH:
//   - CSRF chỉ khả thi khi browser TỰ ĐỘNG đính kèm credential (cookie) vào request.
//   - Bearer token trong Authorization header KHÔNG bị tự động gửi bởi browser → miễn nhiễm CSRF.
//   - API client (mobile, CLI, SDK) dùng Bearer token → không cần CSRF guard.
//   - Browser session (Admin UI) dùng cookie → phải guard chống CSRF.
//   → Middleware này CHỈ áp dụng cho các request dùng cookie auth, bỏ qua Bearer.
//
// 🚫 LÝ DO KHÔNG ĐƯA LÊN ENVOY EDGE:
//   - Envoy CSRF filter không có khả năng check "request này có dùng cookie auth không?"
//   - Nó cần biết tên cookie cụ thể của app (AccessTokenName, RefreshTokenName)
//   - Cần phân biệt Bearer vs Cookie auth để tránh false-positive block API client
//   → Đây là application-semantic logic, không phải infrastructure policy.
//
// ⚠️ RACE CONDITION & SECURITY NOTE:
//   - Middleware này stateless, không shared mutable state → an toàn trong HA multi-instance.
//   - `normalizedAllowed` được tính toán một lần lúc khởi tạo middleware (không lock needed).
//   - Toàn bộ string comparison dùng lowercase normalized → tránh bypass bằng case manipulation.
//
// ==============================================================================================

package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"controlplane/pkg/apires"
	cookie "controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

// headerOrigin là tên HTTP header mà browser tự động gửi khi thực hiện cross-origin request.
// Ví dụ: Origin: https://adminui.aurora.local
const (
	headerOrigin = "Origin"
	// headerReferer là fallback khi Origin header không có (một số browser cũ hoặc redirect flow).
	// Chứa full URL của trang nguồn, ta chỉ dùng scheme + host để so sánh.
	headerReferer = "Referer"
)

// cookieAuthNames là tập hợp tên cookie được hệ thống dùng để xác thực phiên làm việc.
// Đây là "nguồn sự thật" để xác định một request có đang dùng cookie-based auth hay không.
//
// Bao gồm 2 nhóm auth model:
//
//  1. User/Platform plane (AuthService):
//     - AccessTokenName  ("access_token")  → JWT access token của user session.
//     - RefreshTokenName ("refresh_token") → refresh token để gia hạn user session.
//
//  2. Admin plane — Trinity Fragment model (AdminAPIKeyService):
//     Admin session không dùng 1 token đơn mà dùng 3 mảnh (Fragment Token):
//     - AdminAPITokenName ("access_token") → JWT admin token ngắn hạn (alias cùng giá trị với AccessTokenName).
//     - AccessKeyName     ("access_key")   → định danh phiên admin, lưu trong Redis.
//     - AccessSecretName  ("access_secret")→ bí mật phiên admin, chỉ lưu dạng hash trong Redis.
//     Middleware auth phải xác thực cả 3 mảnh khớp nhau mới coi phiên là hợp lệ.
//
// Lưu ý:
//   - Tất cả tên cookie tham chiếu từ pkg/constant/cookie.go — không hardcode string.
//   - Chỉ cần tìm thấy ÍT NHẤT 1 cookie trong danh sách → xác định là cookie-auth request.
var cookieAuthNames = map[string]struct{}{
	// User/Platform session cookies.
	cookie.AccessTokenName:  {},
	cookie.RefreshTokenName: {},
	// Admin Trinity Fragment session cookies.
	cookie.AccessKeyName:    {},
	cookie.AccessSecretName: {},
}

// CookieOriginGuard là CSRF middleware bảo vệ các endpoint sử dụng cookie authentication.
//
// Guard logic thực hiện theo thứ tự 3 điều kiện kích hoạt (fast-path exits):
//
//  1. Chỉ áp dụng cho UNSAFE HTTP methods (POST, PUT, PATCH, DELETE).
//     GET/HEAD/OPTIONS là safe methods, browser không thể dùng để thực hiện CSRF side-effects.
//
//  2. Chỉ áp dụng khi request ĐANG dùng cookie authentication.
//     Bearer token requests được miễn vì browser không tự động gửi Authorization header.
//
//  3. Origin/Referer của request phải nằm trong danh sách allowedOrigins hoặc
//     khớp với Host của server (same-origin request).
//
// Callsite: app.go → engine.Use(middleware.CookieOriginGuard(cfg.App.AllowedOrigins))
//
// Security contract:
//   - allowedOrigins được normalize thành lowercase scheme://host một lần lúc init.
//   - Không có origin/referer = reject (missing credential source = suspicious).
//   - Origin không hợp lệ = reject với 403 Forbidden + security log.
func CookieOriginGuard(allowedOrigins []string) gin.HandlerFunc {
	// Pre-compute normalized allowed origins một lần duy nhất lúc middleware được khởi tạo.
	// Tránh lặp lại normalize trên mỗi request → zero allocation per request.
	normalizedAllowed := normalizeAllowedOrigins(allowedOrigins)

	return func(c *gin.Context) {
		// Nil guard: phòng ngừa nil pointer trong môi trường test hoặc edge case.
		if c == nil || c.Request == nil {
			c.Next()
			return
		}

		// Fast-path 1: Safe methods (GET, HEAD, OPTIONS) không thể gây CSRF side-effects.
		// Browser form CSRF attacks luôn dùng POST; XHR/fetch CSRF phải qua preflight (đã bị CORS block ở Envoy).
		if !isUnsafeMethod(c.Request.Method) {
			c.Next()
			return
		}

		// Fast-path 2: Request không dùng cookie auth → không có nguy cơ CSRF.
		// API clients dùng Bearer token (mobile app, SDK, CLI) không cần CSRF protection.
		if !usesCookieAuth(c.Request) {
			c.Next()
			return
		}

		// Trích xuất và normalize origin của request từ Origin header (ưu tiên) hoặc Referer (fallback).
		// Nếu không có cả hai → suspicious, có thể là direct curl request không hợp lệ.
		origin, ok := extractRequestOrigin(c.Request)
		if !ok {
			logger.HandlerWarn(c, "security.origin", nil, "missing origin/referer for cookie-auth unsafe request")
			apires.RespondForbidden(c, "forbidden")
			c.Abort()
			return
		}

		// Kiểm tra origin có nằm trong whitelist cho phép không.
		// Nếu origin không hợp lệ → block với 403 Forbidden và ghi security log để monitor.
		if !isAllowedOrigin(origin, normalizedAllowed, c.Request.Host) {
			logger.HandlerWarn(c, "security.origin", nil, "origin not allowed for cookie-auth unsafe request")
			apires.RespondForbidden(c, "forbidden")
			c.Abort()
			return
		}

		// Tất cả kiểm tra pass → cho phép request tiếp tục xuống các handler phía sau.
		c.Next()
	}
}

// usesCookieAuth xác định request hiện tại có đang dùng cookie-based authentication không.
//
// Quy tắc phán định (theo thứ tự ưu tiên):
//
//  1. Nếu có Authorization: Bearer <token> → KHÔNG phải cookie auth.
//     Bearer token là explicit credential, browser không tự động gửi → miễn CSRF.
//     Lưu ý: Chỉ check prefix "bearer " (case-insensitive), không validate token value.
//
//  2. Nếu request có cookie thuộc cookieAuthNames → LÀ cookie auth → cần CSRF guard.
//
//  3. Không có cả hai → không phải cookie auth → không cần guard.
//
// Security note: Không cần validate giá trị cookie ở đây; chỉ cần biết loại auth method
// đang được dùng. Việc validate token được thực hiện bởi AuthMiddleware phía sau.
func usesCookieAuth(r *http.Request) bool {
	if r == nil {
		return false
	}

	// Ưu tiên kiểm tra Bearer token trước: nếu có Bearer header → bỏ qua CSRF check.
	// Đây là ưu tiên hàng đầu vì Bearer và Cookie có thể cùng tồn tại trong một request
	// (ví dụ: browser gửi cả hai), nhưng Bearer luôn được ưu tiên về mặt security.
	if bearer, ok := r.Header["Authorization"]; ok {
		for _, raw := range bearer {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "bearer ") {
				// Request dùng Bearer token → không phải cookie-auth → không cần CSRF guard.
				return false
			}
		}
	}

	// Kiểm tra các session cookie của hệ thống.
	// Sử dụng set lookup (O(1)) thay vì linear scan để tối ưu performance.
	cookies := r.Cookies()
	for _, ck := range cookies {
		if ck == nil {
			continue
		}
		if _, ok := cookieAuthNames[strings.TrimSpace(ck.Name)]; ok {
			// Tìm thấy ít nhất một auth cookie → request đang dùng cookie auth → cần CSRF guard.
			return true
		}
	}

	// Không có Bearer, không có auth cookie → request ẩn danh hoặc dùng cơ chế auth khác.
	return false
}

// isUnsafeMethod xác định HTTP method có khả năng gây side-effects (thay đổi state) không.
//
// Theo RFC 7231:
//   - Safe methods: GET, HEAD, OPTIONS, TRACE → read-only, không thay đổi state server.
//   - Unsafe methods: POST, PUT, PATCH, DELETE → có thể modify/delete resources.
//
// Chỉ unsafe methods mới cần CSRF protection vì CSRF attacks mục tiêu vào state-changing operations.
func isUnsafeMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// normalizeAllowedOrigins chuyển đổi danh sách origin strings thành một map lookup tối ưu.
//
// Mỗi origin được normalize thành dạng chuẩn lowercase "scheme://host" để đảm bảo
// so sánh nhất quán và tránh bypass thông qua case manipulation (VD: HTTPS://AdminUI.Aurora.local).
//
// Origin không hợp lệ (thiếu scheme, thiếu host, scheme không phải http/https) bị bỏ qua.
// Map được tính toán một lần lúc khởi tạo middleware, không cần lock vì read-only sau đó.
func normalizeAllowedOrigins(origins []string) map[string]struct{} {
	out := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		normalized, ok := normalizeOrigin(origin)
		if !ok {
			continue
		}
		out[normalized] = struct{}{}
	}
	return out
}

// extractRequestOrigin lấy origin của request, theo thứ tự ưu tiên:
//
//  1. Origin header (ưu tiên cao nhất): Browser gửi header này cho mọi cross-origin request.
//     Đây là cách chuẩn và đáng tin cậy nhất để xác định nguồn gốc request.
//
//  2. Referer header (fallback): Một số browser cũ hoặc redirect flow không gửi Origin header.
//     Referer chứa full URL (VD: https://adminui.aurora.local/zones/new), ta chỉ dùng
//     scheme + host để so sánh, bỏ qua path, query string, fragment.
//
// Trả về (normalizedOrigin, true) nếu tìm thấy origin hợp lệ.
// Trả về ("", false) nếu không có cả hai hoặc không parse được.
func extractRequestOrigin(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}

	// Thử Origin header trước: đây là source of truth cho cross-origin requests.
	if origin, ok := normalizeOrigin(strings.TrimSpace(r.Header.Get(headerOrigin))); ok {
		return origin, true
	}

	// Fallback sang Referer header: parse để chỉ lấy scheme + host.
	referer := strings.TrimSpace(r.Header.Get(headerReferer))
	if referer == "" {
		// Không có cả Origin lẫn Referer → không xác định được nguồn gốc.
		return "", false
	}

	u, err := url.Parse(referer)
	if err != nil || strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		// Referer không parse được hoặc thiếu scheme/host → không hợp lệ.
		return "", false
	}
	// Reconstruct từ scheme + host, bỏ qua path/query của Referer URL.
	return normalizeOrigin(u.Scheme + "://" + u.Host)
}

// normalizeOrigin parse và normalize một origin string thành dạng chuẩn "scheme://host" (lowercase).
//
// Quy tắc:
//   - Chỉ chấp nhận scheme "http" hoặc "https". Scheme khác (ftp, file, v.v.) bị reject.
//   - Host phải không rỗng.
//   - Toàn bộ kết quả được lowercase để đảm bảo so sánh case-insensitive.
//   - Port nếu có sẽ được giữ nguyên trong host (VD: localhost:5173).
//
// Trả về ("", false) cho mọi input không hợp lệ thay vì panic/error để middleware
// có thể xử lý gracefully.
func normalizeOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	host := strings.ToLower(strings.TrimSpace(u.Host))
	// Chỉ cho phép web-safe schemes; reject ftp://, file://, data://, v.v.
	if (scheme != "http" && scheme != "https") || host == "" {
		return "", false
	}

	return scheme + "://" + host, true
}

// isAllowedOrigin kiểm tra origin có được phép thực hiện request không.
//
// Có 2 điều kiện để được chấp nhận (OR logic):
//
//  1. Origin nằm trong danh sách `allowed` (APP_ALLOWED_ORIGINS trong .env):
//     Ví dụ: https://adminui.aurora.local
//
//  2. Origin host khớp với Host của request (same-origin request):
//     Ví dụ: request đến controlplane.aurora.local từ controlplane.aurora.local.
//     Cho phép server-to-server và same-domain requests mà không cần liệt kê tường minh.
//     So sánh case-insensitive bằng strings.EqualFold.
//
// Lưu ý: `reqHost` là `c.Request.Host` sau khi đã qua trusted proxy processing.
func isAllowedOrigin(origin string, allowed map[string]struct{}, reqHost string) bool {
	if origin == "" {
		return false
	}

	// Kiểm tra whitelist configured origins (O(1) map lookup).
	if _, ok := allowed[origin]; ok {
		return true
	}

	// Same-origin fallback: origin host == request host → luôn cho phép.
	// Xử lý trường hợp server tự gọi chính nó hoặc service mesh internal requests.
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	return strings.EqualFold(strings.TrimSpace(u.Host), strings.TrimSpace(reqHost))
}
