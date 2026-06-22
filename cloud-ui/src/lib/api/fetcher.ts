import { controlplaneBaseURL } from "./config";

export type APIError = {
  status: number;
  message: string;
};

type FetchJSONOptions = {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  headers?: HeadersInit;
  credentials?: RequestCredentials;
  signal?: AbortSignal;
};

// =====================================================================================
// SESSION LIFECYCLE MANAGER
// =====================================================================================
// Quản lý vòng đời phiên làm việc:
//
// Kiểu 1 — Trinity Refresh (sliding session):
//   Được xử lý tự động và trong suốt (transparent) tại Rust ACL (ext_authz) trên Envoy Gateway.
//   Client không cần gọi endpoint riêng — Envoy tự xoay vòng cookies qua Set-Cookie header.
//
// Kiểu 2 — Opaque Refresh Token (session recovery):
//   Khi gặp 401 và có refresh_token cookie → thử gọi /api/v1/auth/refresh
//   để tạo phiên mới hoàn toàn, không cần nhập lại mật khẩu.
// =====================================================================================

// [COMMENT]: Semaphore ngăn chặn race condition khi nhiều request đồng thời trigger opaque refresh.
// Chỉ 1 request duy nhất thực hiện refresh tại một thời điểm.
let opaqueRefreshInFlight: Promise<boolean> | null = null;

// [COMMENT]: Danh sách path không trigger session recovery để tránh vòng lặp vô hạn.
const AUTH_PATHS = ["/auth/login", "/auth/register", "/auth/refresh"];

function isAuthPath(path: string): boolean {
  return AUTH_PATHS.some((p) => path.includes(p));
}



// [COMMENT]: Gọi opaque refresh (Kiểu 2) khi trinity đã chết nhưng refresh_token cookie còn sống trực tiếp qua Envoy.
// Trả về true nếu phục hồi phiên thành công, false nếu cần redirect login.
async function performOpaqueRefresh(): Promise<boolean> {
  try {
    const response = await fetch("/api/v1/auth/refresh", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
    });
    return response.ok || response.status === 204;
  } catch {
    return false;
  }
}

// ============================================================================
// 🛡️ XSSI RESPONSE WRAPPER (BỘ HOÀN TRẢ PHẢN HỒI CHỐNG XSSI CHO CLOUD UI)
// ============================================================================
// [COMMENT]: Lớp wrapper bao bọc đối tượng Response gốc từ trình duyệt nhằm tự động
// phát hiện và cắt bỏ tiền tố bảo mật ")]}',\n" (XSSI prefix) trước khi thực hiện
// phân tích cú pháp (parse) nội dung JSON.
class XSSIResponse {
  private readonly raw: Response;
  private memoizedText: Promise<string> | null = null;

  constructor(raw: Response) {
    this.raw = raw;
  }

  // [COMMENT]: Đọc nội dung phản hồi thô dưới dạng text và cắt bỏ tiền tố XSSI
  // nếu được trả về từ phía máy chủ để tránh lỗi JSON Syntax Error.
  text(): Promise<string> {
    if (this.memoizedText) return this.memoizedText;
    this.memoizedText = this.raw.text().then((text) => {
      // Backend thường ghi nhận tiền tố ")]}',\n" (6 ký tự)
      if (text.startsWith(")]}',\n")) return text.slice(6);
      // Phương án phòng ngự nếu không có ký tự xuống dòng (5 ký tự)
      if (text.startsWith(")]}',")) return text.slice(5);
      return text;
    });
    return this.memoizedText;
  }

  // [COMMENT]: Parse dữ liệu sang JSON sau khi đã lọc bỏ tiền tố bảo mật.
  json(): Promise<unknown> {
    return this.text().then((text) => JSON.parse(text));
  }

  // [COMMENT]: Uỷ quyền (delegate) các phương thức và thuộc tính tiêu chuẩn sang Response gốc.
  arrayBuffer(): Promise<ArrayBuffer> { return this.raw.arrayBuffer(); }
  blob(): Promise<Blob> { return this.raw.blob(); }
  formData(): Promise<FormData> { return this.raw.formData(); }

  get status(): number { return this.raw.status; }
  get statusText(): string { return this.raw.statusText; }
  get ok(): boolean { return this.raw.ok; }
  get redirected(): boolean { return this.raw.redirected; }
  get type(): ResponseType { return this.raw.type; }
  get url(): string { return this.raw.url; }
  get bodyUsed(): boolean { return this.raw.bodyUsed; }
  get headers(): Headers { return this.raw.headers; }
  get body(): ReadableStream<Uint8Array> | null { return this.raw.body as unknown as ReadableStream<Uint8Array> | null; }
  bytes(): Promise<Uint8Array> {
    if (typeof this.raw.bytes === "function") {
      return this.raw.bytes();
    }
    throw new Error("Response.bytes is not supported in this environment");
  }
  clone(): XSSIResponse { return new XSSIResponse(this.raw.clone()); }
}

// [COMMENT]: Hàm tiện ích để bọc đối tượng Response gốc bằng XSSIResponse
function wrapResponse(raw: Response): Response {
  return new XSSIResponse(raw) as unknown as Response;
}

export async function fetchJSON<T>(
  path: string,
  options: FetchJSONOptions = {},
): Promise<T> {
  const {
    method = "GET",
    body,
    headers,
    credentials = "same-origin",
    signal,
  } = options;

  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  // [COMMENT]: Thực hiện cuộc gọi HTTP gốc và bọc kết quả lại bằng wrapResponse để kích hoạt lọc XSSI.
  const response = wrapResponse(
    await fetch(`${controlplaneBaseURL}${normalizedPath}`, {
      method,
      headers: {
        "Content-Type": "application/json",
        ...(headers || {}),
      },
      credentials,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
  );

  // =========================================================================
  // KIỂU 2: OPAQUE REFRESH — Session Recovery khi 401 + không phải auth path
  // =========================================================================
  // [COMMENT]: Khi nhận 401 và request không phải auth endpoint, thử dùng opaque refresh token
  // để tạo phiên mới. Nếu thành công → retry request gốc. Nếu thất bại → dispatch iam:unauthorized.
  if (response.status === 401 && !isAuthPath(path)) {
    // [COMMENT]: Dedup — chỉ 1 opaque refresh chạy đồng thời. Các request khác chờ kết quả.
    if (!opaqueRefreshInFlight) {
      opaqueRefreshInFlight = performOpaqueRefresh().finally(() => {
        opaqueRefreshInFlight = null;
      });
    }
    const recovered = await opaqueRefreshInFlight;

    if (recovered) {
      // [COMMENT]: Phục hồi phiên thành công → retry request gốc 1 lần duy nhất, kết quả cũng được lọc XSSI.
      const retryResponse = wrapResponse(
        await fetch(`${controlplaneBaseURL}${normalizedPath}`, {
          method,
          headers: {
            "Content-Type": "application/json",
            ...(headers || {}),
          },
          credentials,
          body: body === undefined ? undefined : JSON.stringify(body),
          signal,
        })
      );

      if (retryResponse.ok || retryResponse.status === 204) {

        if (retryResponse.status === 204) {
          return undefined as T;
        }
        return (await retryResponse.json()) as T;
      }

      // Retry thất bại → 401 thật sự, dispatch unauthorized
      if (retryResponse.status === 401 && typeof window !== "undefined") {
        window.dispatchEvent(new CustomEvent("iam:unauthorized"));
      }

      let retryMessage = "request failed";
      try {
        const payload = (await retryResponse.json()) as { message?: string };
        if (typeof payload?.message === "string" && payload.message.trim() !== "") {
          retryMessage = payload.message;
        }
      } catch {
        retryMessage = retryResponse.statusText || retryMessage;
      }
      throw { status: retryResponse.status, message: retryMessage } as APIError;
    }

    // [COMMENT]: Opaque refresh thất bại (không có refresh token hoặc đã hết hạn) → redirect login.
    if (typeof window !== "undefined") {
      window.dispatchEvent(new CustomEvent("iam:unauthorized"));
    }
  }

  if (!response.ok) {
    // [COMMENT]: Phát hiện lỗi 401 Unauthorized từ hệ thống. Nếu không phải yêu cầu đăng nhập,
    // phát tín hiệu toàn cục thông qua Event System để buộc Client Reset phiên làm việc ngay lập tức.
    if (response.status === 401 && !path.includes("/auth/login") && typeof window !== "undefined") {
      window.dispatchEvent(new CustomEvent("iam:unauthorized"));
    }

    let message = "request failed";
    try {
      const payload = (await response.json()) as { message?: string };
      if (typeof payload?.message === "string" && payload.message.trim() !== "") {
        message = payload.message;
      }
    } catch {
      message = response.statusText || message;
    }

    throw {
      status: response.status,
      message,
    } as APIError;
  }


  if (response.status === 204) {
    return undefined as T;
  }

  return (await response.json()) as T;
}
