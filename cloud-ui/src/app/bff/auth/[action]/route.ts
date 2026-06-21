import { NextResponse } from "next/server";

// [COMMENT]: Vô hiệu hoá kiểm tra chứng chỉ SSL tự ký của Envoy trong môi trường Node.js Server của BFF.
// Điều này là bắt buộc vì Envoy sử dụng cert tự ký trong môi trường phát triển local.
process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";

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
  const cookieHeader = request.headers.get("cookie") || "";
  console.log(`[BFF Proxy] Incoming POST request for action: "${action}", cookie length: ${cookieHeader.length}, cookies: "${cookieHeader}"`);

  // Chỉ cho phép xử lý 2 hành động liên quan đến làm mới phiên
  if (action !== "trinity-refresh" && action !== "refresh") {
    console.warn(`[BFF Proxy] Action "${action}" not allowed, returning 404`);
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

  // [COMMENT]: Lấy URL của Envoy từ biến môi trường để kết nối đến Controlplane, không sử dụng cơ chế fallback.
  const internalBaseUrl = process.env.NEXT_PUBLIC_CONTROLPLANE_URL || "https://envoy:8443";

  try {
    // [COMMENT]: Gọi qua Ingress Gateway được cấu hình từ biến môi trường
    backendResponse = await fetch(`${internalBaseUrl}/api/v1/auth/${action}`, {
      method: "POST",
      headers,
      body: requestBody,
      ...({ duplex: "half" } as unknown as RequestInit),
    });
  } catch (error) {
    console.error(`[BFF Proxy Critical Error] Failed to connect to backend endpoint ${internalBaseUrl}`, error);
    return new NextResponse(
      JSON.stringify({ message: "BFF Auth proxy failed to connect to controlplane cluster" }),
      {
        status: 502,
        headers: { "Content-Type": "application/json" },
      }
    );
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

// [COMMENT]: API Route Handler xử lý GET request chuyển tiếp tác vụ kiểm tra phiên làm việc ngầm (Session Check).
export async function GET(request: Request, { params }: RouteParams) {
  const { action } = await params;
  const cookieHeader = request.headers.get("cookie") || "";
  console.log(`[BFF Proxy] Incoming GET request for action: "${action}", cookie length: ${cookieHeader.length}, cookies: "${cookieHeader}"`);

  // Chỉ cho phép xử lý hành động lấy thông tin phiên
  if (action !== "session") {
    console.warn(`[BFF Proxy] Action "${action}" not allowed for GET, returning 404`);
    return new NextResponse("Not Found", { status: 404 });
  }

  // [COMMENT]: Bản sao của Headers nhận được từ Client để chuyển tiếp (bao gồm Cookie phiên)
  const headers = new Headers();
  request.headers.forEach((value, key) => {
    headers.set(key, value);
  });

  // Thiết lập Host header để Envoy Ingress Gateway định tuyến chính xác đến cloud vhost (cloud.aurora.local)
  const clientHost = request.headers.get("host") || "cloud.aurora.local";
  headers.set("host", clientHost);

  let backendResponse: Response;

  // [COMMENT]: Lấy URL của Envoy từ biến môi trường để kết nối đến Controlplane, không sử dụng cơ chế fallback.
  const internalBaseUrl = process.env.NEXT_PUBLIC_CONTROLPLANE_URL || "https://envoy:8443";

  try {
    // [COMMENT]: Gọi qua Ingress Gateway được cấu hình từ biến môi trường
    backendResponse = await fetch(`${internalBaseUrl}/api/v1/auth/${action}`, {
      method: "GET",
      headers,
    });
  } catch (error) {
    console.error(`[BFF Proxy Critical Error] Failed to connect to backend endpoint ${internalBaseUrl}`, error);
    return new NextResponse(
      JSON.stringify({ message: "BFF Auth proxy failed to connect to controlplane cluster" }),
      {
        status: 502,
        headers: { "Content-Type": "application/json" },
      }
    );
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
