import { NextResponse } from "next/server";

// [COMMENT]: Định nghĩa cấu trúc params cho Route Handler trong Next.js 16.
// Trong Next.js 15/16, params là một Promise và cần được giải quyết bằng await.
type RouteParams = {
  params: Promise<{
    action: string;
  }>;
};

// [COMMENT]: API Route Handler xử lý POST request chuyển tiếp các tác vụ xác thực ngầm (Refresh / Trinity Refresh).
export async function POST(request: Request, { params }: RouteParams) {
  const { action } = await params;

  // Chỉ cho phép xử lý 2 hành động liên quan đến làm mới phiên
  if (action !== "trinity-refresh" && action !== "refresh") {
    return new NextResponse("Not Found", { status: 404 });
  }

  // [COMMENT]: Bản sao của Headers nhận được từ Client để chuyển tiếp (bao gồm Cookie phiên cũ)
  const headers = new Headers();
  request.headers.forEach((value, key) => {
    headers.set(key, value);
  });

  // Thiết lập Host header để Envoy Ingress Gateway định tuyến chính xác đến cloud vhost (cloud.aurora.local)
  const clientHost = request.headers.get("host") || "cloud.aurora.local";
  headers.set("host", clientHost);

  // Đọc request body nếu có dưới dạng ArrayBuffer để chuyển tiếp an toàn (ví dụ trong các request POST có payload)
  const requestBody = request.body ? await request.arrayBuffer() : undefined;

  let backendResponse: Response;

  try {
    // [COMMENT]: Thử kết nối qua Envoy nội bộ trong mạng Docker (HA/Production Mode)
    backendResponse = await fetch(`http://envoy:8080/api/v1/auth/${action}`, {
      method: "POST",
      headers,
      body: requestBody,
      ...({ duplex: "half" } as unknown as RequestInit),
    });
  } catch {
    // [COMMENT]: Cơ chế Fallback tự phục hồi (Self-Healing) - nếu không kết nối được tới http://envoy:8080
    // (ví dụ khi chạy môi trường development ngoài Docker trên máy host), chuyển sang gọi localhost:28000.
    console.warn(`[BFF Proxy] Envoy endpoint http://envoy:8080 unreachable for ${action}, falling back to localhost...`);
    try {
      backendResponse = await fetch(`http://localhost:28000/api/v1/auth/${action}`, {
        method: "POST",
        headers,
        body: requestBody,
        ...({ duplex: "half" } as unknown as RequestInit),
      });
    } catch (fallbackError) {
      console.error(`[BFF Proxy Critical Error] Both primary and fallback endpoints failed for action: ${action}`, fallbackError);
      return new NextResponse(
        JSON.stringify({ message: "BFF Auth proxy failed to connect to controlplane" }),
        {
          status: 502,
          headers: { "Content-Type": "application/json" },
        }
      );
    }
  }

  // [COMMENT]: Chuẩn bị headers phản hồi chuyển tiếp ngược lại trình duyệt (bao gồm các header Set-Cookie mới)
  const responseHeaders = new Headers();
  backendResponse.headers.forEach((value, key) => {
    responseHeaders.set(key, value);
  });

  // Đọc nội dung phản hồi thô (đã kèm XSSI prefix từ Go backend)
  const responseBody = await backendResponse.text();

  return new NextResponse(responseBody, {
    status: backendResponse.status,
    statusText: backendResponse.statusText,
    headers: responseHeaders,
  });
}
