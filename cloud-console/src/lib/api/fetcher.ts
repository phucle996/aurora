import { controlplaneBaseURL } from "./config";

export type APIError = {
  status: number;
  message: string;
};

export type FetchJSONOptions = {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  serializedBody?: string;
  headers?: HeadersInit;
  credentials?: RequestCredentials;
  signal?: AbortSignal;
};

// =====================================================================================
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
    serializedBody,
    headers,
    credentials = "same-origin",
    signal,
  } = options;

  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  const requestHeaders = new Headers(headers);
  if (!requestHeaders.has("Content-Type")) {
    requestHeaders.set("Content-Type", "application/json");
  }
  // [COMMENT]: Thực hiện cuộc gọi HTTP gốc và bọc kết quả lại bằng wrapResponse để kích hoạt lọc XSSI.
  const response = wrapResponse(
    await fetch(`${controlplaneBaseURL}${normalizedPath}`, {
      method,
      headers: requestHeaders,
      credentials,
      // [COMMENT]: Critical fetcher ký đúng chuỗi này; tuyệt đối không stringify lần hai sau khi đã ký.
      body: serializedBody !== undefined
        ? serializedBody
        : body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
  );



  if (!response.ok) {
    // [COMMENT]: Phát hiện lỗi 401 Unauthorized từ hệ thống. Nếu không phải yêu cầu đăng nhập,
    // phát tín hiệu toàn cục thông qua Event System để buộc Client Reset phiên làm việc ngay lập tức.
    if (response.status === 401 && !path.includes("/auth/login") && typeof window !== "undefined") {
      window.dispatchEvent(new CustomEvent("iam:unauthorized"));
    }

    let message = "request failed";
    try {
      /* [COMMENT]: Hỗ trợ parse cả message và error_message (iam: invalid credentials) từ backend trả về */
      const payload = (await response.json()) as { message?: string; error_message?: string };
      if (typeof payload?.error_message === "string" && payload.error_message.trim() !== "") {
        message = payload.error_message;
      } else if (typeof payload?.message === "string" && payload.message.trim() !== "") {
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
