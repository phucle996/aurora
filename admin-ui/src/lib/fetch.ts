// =============================================================================
// fetch.ts — Admin UI HTTP Client với XSSI Protection & Session Resilience
//
// Tổng quan kiến trúc:
//   Browser → Fetch() → request() → native fetch()
//                              ↓
//                       wrapResponse()     ← strip XSSI prefix ")]}',\n"
//                              ↓
//               [401?] → Reactive Self-Healing (token refresh)
//
// XSSI (Cross-Site Script Inclusion) Protection:
//   Backend middleware prepends ")]}',\n" trước mọi JSON response để ngăn
//   JSON array/object hijacking. Client phải strip prefix này trước khi parse.
//   Xem: controlplane/internal/http/middleware/admin_xssi.go
//
// Lưu ý quan trọng — Tại sao không dùng Proxy cho wrapResponse():
//   Native Response getters (ok, status, headers, url, bodyUsed, ...) đều
//   dùng C++ internal slots và kiểm tra IsResponse(this). Khi gọi qua Proxy
//   với this = proxy (không phải Response thật), chúng ném
//   "TypeError: Illegal invocation" → bị catch → authenticated = false → redirect login.
//   Giải pháp: dùng XSSIResponse class — delegate trực tiếp sang raw Response,
//   override chỉ text() và json() để strip prefix.
// =============================================================================

import { emitAdminUnauthorized } from '@/lib/admin-auth-events'

// URL gốc của Controlplane API, đọc từ biến môi trường Vite tại build-time.
// Nếu không set → fallback thành '' → các request sẽ dùng relative URL (same-origin).
const ControlplaneURL = (import.meta.env.VITE_CONTROLPLANE_API_BASE_URL ?? '').trim()

// ---------------------------------------------------------------------------
// URL Utilities
// ---------------------------------------------------------------------------

/**
 * Xóa trailing slash khỏi baseURL để tránh double-slash khi ghép với path.
 * Trả về '' nếu baseURL rỗng (same-origin mode).
 */
function normalizeBaseURL(baseURL: string): string {
  if (baseURL === '') return ''
  return baseURL.replace(/\/+$/, '')
}

/**
 * Ghép baseURL + path thành absolute URL.
 *
 * Rules:
 *   - path rỗng   → trả về normalized baseURL
 *   - path là http(s):// → trả về nguyên path (absolute external URL)
 *   - path relative → thêm leading slash, ghép với normalized baseURL
 *   - baseURL rỗng → trả về path (same-origin relative URL)
 */
function toAbsoluteURL(baseURL: string, path: string): string {
  const trimmedPath = path.trim()
  if (trimmedPath === '') return normalizeBaseURL(baseURL)
  if (/^https?:\/\//i.test(trimmedPath)) return trimmedPath

  const normalizedPath = trimmedPath.startsWith('/') ? trimmedPath : `/${trimmedPath}`
  const normalizedBaseURL = normalizeBaseURL(baseURL)
  if (normalizedBaseURL === '') return normalizedPath
  return `${normalizedBaseURL}${normalizedPath}`
}

// ---------------------------------------------------------------------------
// XSSI Response Wrapper
// ---------------------------------------------------------------------------

/**
 * XSSIResponse — Wrapper an toàn cho native Response để strip XSSI prefix.
 *
 * Tại sao KHÔNG dùng Proxy:
 *   Proxy trap `get(target, prop, receiver)` gọi getter với `this = receiver`
 *   (= proxy object). Tất cả getter native của Response (ok, status, headers,
 *   url, redirected, bodyUsed, type, ...) đều được implement trong C++ và
 *   kiểm tra internal slot:
 *     if (!IsResponse(this)) throw new TypeError('Illegal invocation')
 *   Proxy không pass được IsResponse check → TypeError → bị catch phía trên
 *   → authenticated = false → redirect login dù session hợp lệ.
 *
 * Giải pháp: delegate TẤT CẢ properties sang raw response gốc,
 * chỉ override text() và json() để thực hiện XSSI stripping.
 *
 * Implement Response interface để TypeScript đảm bảo type safety.
 */
// Không dùng `implements Response` vì một số getter của Response interface
// (như body, bytes) có type definitions không tương thích với generic variants
// của TypeScript lib, gây lỗi type mismatch dù behavior hoàn toàn đúng.
// Thay vào đó, wrapResponse() cast sang Response bằng `as unknown as Response`.
//
// Không dùng parameter property `constructor(private readonly raw)` vì
// erasableSyntaxOnly: true không cho phép TypeScript syntax không erasable.
class XSSIResponse {
  // Memoize text body để tránh đọc stream nhiều lần.
  // ReadableStream của Response chỉ có thể consume 1 lần.
  private readonly raw: Response
  private memoizedText: Promise<string> | null = null

  constructor(raw: Response) {
    this.raw = raw
  }

  // --- XSSI-aware body consumers -------------------------------------------

  /**
   * Đọc body response, tự động strip XSSI prefix ")]}',\n" nếu có.
   *
   * Prefix variants được hỗ trợ:
   *   ")]}',\n"  (6 chars) — chuẩn có newline, Go backend ghi ra
   *   ")]}',\\"   (5 chars) — variant không có newline (defensive fallback)
   *
   * Memoized: stream chỉ được đọc 1 lần, các lần gọi sau trả về cached Promise.
   */
  text(): Promise<string> {
    if (this.memoizedText) return this.memoizedText
    this.memoizedText = this.raw.text().then((text) => {
      // Go backend: w.ResponseWriter.Write([]byte(")]}',\n"))
      if (text.startsWith(")]}',\n")) return text.slice(6)
      // Defensive: nếu không có newline (edge case)
      if (text.startsWith(")]}',")) return text.slice(5)
      return text
    })
    return this.memoizedText
  }

  /**
   * Parse body response thành JSON, sau khi đã strip XSSI prefix.
   * Delegate sang text() để đảm bảo strip luôn được áp dụng.
   */
  json(): Promise<unknown> {
    return this.text().then((text) => JSON.parse(text))
  }

  // --- Body readers (không cần XSSI strip) ----------------------------------

  /** Đọc body dưới dạng ArrayBuffer (binary data). */
  arrayBuffer(): Promise<ArrayBuffer> { return this.raw.arrayBuffer() }

  /** Đọc body dưới dạng Blob. */
  blob(): Promise<Blob> { return this.raw.blob() }

  /** Đọc body dưới dạng FormData (multipart responses). */
  formData(): Promise<FormData> { return this.raw.formData() }

  // --- Response metadata (delegate sang raw) --------------------------------

  /** HTTP status code (200, 401, 500, ...). */
  get status(): number { return this.raw.status }

  /** HTTP status message ("OK", "Unauthorized", ...). */
  get statusText(): string { return this.raw.statusText }

  /**
   * true nếu status trong [200, 299] — request thành công.
   * QUAN TRỌNG: Đây là lý do class wrapper an toàn hơn Proxy —
   * getter này gọi trực tiếp trên this.raw (Response thật), không qua Proxy.
   */
  get ok(): boolean { return this.raw.ok }

  /** true nếu request bị redirect. */
  get redirected(): boolean { return this.raw.redirected }

  /** Response type: "basic", "cors", "opaque", "error", ... */
  get type(): ResponseType { return this.raw.type }

  /** URL cuối cùng sau redirect. */
  get url(): string { return this.raw.url }

  /** true nếu body đã được consumed (đọc rồi). */
  get bodyUsed(): boolean { return this.raw.bodyUsed }

  /** HTTP response headers. */
  get headers(): Headers { return this.raw.headers }

  /**
   * ReadableStream của body (nếu cần stream trực tiếp).
   * null nếu body đã được consumed hoặc response là null body.
   * Cast cần thiết vì lib.dom.d.ts dùng Uint8Array<ArrayBuffer> (strict)
   * trong khi this.raw.body thực tế trả về Uint8Array<ArrayBufferLike>.
   */
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  get body(): ReadableStream<Uint8Array> | null { return this.raw.body as any }

  /**
   * Đọc body dưới dạng Uint8Array (Fetch API method mới hơn blob/arrayBuffer).
   * Delegate trực tiếp sang raw response.
   */
  bytes(): Promise<Uint8Array> { return this.raw.bytes() }

  /**
   * Clone response để đọc body nhiều lần.
   * Trả về XSSIResponse mới để giữ XSSI stripping behavior trên clone.
   */
  clone(): XSSIResponse { return new XSSIResponse(this.raw.clone()) }
}

/**
 * Wrap raw Response vào XSSIResponse để enable XSSI prefix stripping.
 * Mọi response từ Controlplane API đều phải đi qua hàm này.
 *
 * Cast sang Response (qua unknown) vì XSSIResponse không `implements Response`
 * (tránh type conflict với các Response member như body, bytes).
 * Toàn bộ API surface đã được delegate đúng, nên cast này an toàn.
 */
function wrapResponse(raw: Response): Response {
  return new XSSIResponse(raw) as unknown as Response
}

// ---------------------------------------------------------------------------
// Core Request Function
// ---------------------------------------------------------------------------

/**
 * Thực hiện HTTP request đến Controlplane API với các tính năng:
 *   1. XSSI prefix stripping (qua wrapResponse)
 *   2. Reactive Self-Healing: tự refresh token khi gặp 401
 *   3. Proactive Silent Refresh: refresh sớm khi session sắp hết hạn
 *
 * @param baseURL  - Base URL của API (từ VITE_CONTROLPLANE_API_BASE_URL)
 * @param input    - Path của endpoint (e.g., '/admin/auth/session')
 * @param init     - RequestInit options (method, headers, body, ...)
 */
function request(baseURL: string, input: string, init?: RequestInit): Promise<Response> {
  const trimmedInput = input.trim()
  // Normalize path để kiểm tra isAuthRoute chính xác
  const normalizedPath = trimmedInput.startsWith('/') ? trimmedInput : `/${trimmedInput}`

  // Luôn gửi cookie (session cookie) kèm request để server authenticate
  const reqInit: RequestInit = {
    credentials: 'include',
    ...init,
  }



  return fetch(toAbsoluteURL(baseURL, input), reqInit).then((rawResponse) => {
    // Bước 1: Wrap response để enable XSSI stripping cho mọi .text()/.json() call
    const response = wrapResponse(rawResponse)

    // Auth endpoints không kích hoạt auto-refresh để tránh vòng lặp vô tận.
    // /admin/auth/session: kiểm tra session → nếu 401 thì logout, không refresh
    // /admin/auth/login, /logout: flow auth riêng
    // /admin/auth/refresh: chính là endpoint refresh, không tự refresh lại
    const isAuthRoute = [
      '/admin/auth/refresh',
      '/admin/auth/login',
      '/admin/auth/logout',
      '/admin/auth/session',
    ].some((route) => normalizedPath.startsWith(route))

    // =========================================================================
    // SRE Admin không sử dụng cơ chế Opaque Refresh Token để khôi phục phiên.
    // Khi nhận phản hồi 401 Unauthorized từ biên (session thực sự hết hạn hoặc bị thu hồi),
    // client trực tiếp phát đi sự kiện đăng xuất để chuyển trạng thái giao diện về unauthenticated.
    // =========================================================================
    if (response.status === 401 && !isAuthRoute) {
      emitAdminUnauthorized()
      return response
    }

    // [COMMENT]: Cơ chế trượt phiên làm việc (sliding session) hiện tại được thực thi tự động và transparent ở Edge (Envoy + Rust ACL)
    // dựa trên JWT TTL còn lại < 900s bằng cách trả về Set-Cookie trong response API. Frontend không cần chủ động gửi yêu cầu refresh.

    return response
  })
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/**
 * Fetch wrapper chính cho toàn bộ Admin UI.
 *
 * Dùng thay cho native fetch() để có:
 *   - XSSI prefix stripping tự động
 *   - Auto token refresh khi 401
 *   - Credentials (cookie) luôn được gửi kèm
 *
 * @example
 *   const resp = await Fetch('/admin/zones')
 *   const data = await resp.json()  // XSSI prefix đã được strip
 */
export function Fetch(input: string, init?: RequestInit): Promise<Response> {
  return request(ControlplaneURL, input, init)
}
