import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

export async function middleware(request: NextRequest) {
  const { pathname } = request.nextUrl;

  // [COMMENT]: Bỏ qua các router tĩnh, assets, public và các route auth công khai để tránh lặp vô hạn.
  if (
    pathname.startsWith("/_next") ||
    pathname.startsWith("/api") ||
    pathname.startsWith("/bff") ||
    pathname.includes(".") ||
    pathname === "/signin" ||
    pathname === "/signup" ||
    pathname === "/reset-password" ||
    pathname === "/forgot-password"
  ) {
    return NextResponse.next();
  }

  const internalBaseUrl = process.env.NEXT_PUBLIC_CONTROLPLANE_URL || "https://envoy:8443";
  const cookieHeader = request.headers.get("cookie") || "";
  // [COMMENT]: Lấy Origin từ biến môi trường NEXT_PUBLIC_APP_ORIGIN (mặc định cấu hình trong .env.local).
  // Khi chạy Server-to-Server trong middleware, fetch không tự đính kèm Origin của trình duyệt client.
  // Do đó, ta truyền thủ công biến này để Go backend (CookieOriginGuard) xác thực request an toàn.
  const clientOrigin = request.headers.get("origin") || process.env.NEXT_PUBLIC_APP_ORIGIN || "https://cloud.aurora.local";

  // [COMMENT]: Hàm xoá sạch session cookies của client và điều hướng về trang signin
  const redirectToSignIn = () => {
    const response = NextResponse.redirect(new URL("/signin", request.url));
    response.cookies.delete("access_token");
    response.cookies.delete("access_key");
    response.cookies.delete("access_secret");
    response.cookies.delete("refresh_token");
    return response;
  };

  try {
    // [COMMENT]: Chuyển tiếp request kiểm tra phiên làm việc (session check) trực tiếp đến Go backend.
    // Next.js đóng vai trò proxy thuần túy để giữ sạch logic bảo mật, không tự parse hay giải mã JWT.
    const sessionRes = await fetch(`${internalBaseUrl}/api/v1/auth/session`, {
      method: "GET",
      headers: {
        "Cookie": cookieHeader,
        "Host": "cloud.aurora.local",
      },
    });

    // 1. Nếu Go backend báo phiên hợp lệ (200 OK)
    if (sessionRes.ok) {
      const response = NextResponse.next();

      // [COMMENT]: Đọc giá trị thời hạn sống của phiên từ Header do Go backend tính toán và trả về.
      const expiresInStr = sessionRes.headers.get("X-Session-Expires-In");
      if (expiresInStr) {
        const expiresIn = parseInt(expiresInStr, 10);
        // Nếu còn ≤ 900 giây -> Kích hoạt Trinity Refresh ngay trên server (Server-to-Server)
        if (!isNaN(expiresIn) && expiresIn <= 900) {
          console.log(`[Middleware Proxy] Session near expiry (${expiresIn}s), proxying Trinity Refresh`);
          const refreshRes = await fetch(`${internalBaseUrl}/api/v1/auth/trinity-refresh`, {
            method: "POST",
            headers: {
              "Cookie": cookieHeader,
              "Host": "cloud.aurora.local",
              "Origin": clientOrigin,
            },
          });

          if (refreshRes.ok) {
            const setCookies = refreshRes.headers.getSetCookie();
            if (setCookies.length > 0) {
              setCookies.forEach((cookieStr) => {
                response.headers.append("Set-Cookie", cookieStr);
              });
            } else {
              const setCookie = refreshRes.headers.get("Set-Cookie");
              if (setCookie) {
                response.headers.set("Set-Cookie", setCookie);
              }
            }
            console.log("[Middleware Proxy] Trinity Refresh proxy succeeded");
          }
        }
      }
      return response;
    }

    // 2. Nếu Go backend báo phiên không hợp lệ (401 Unauthorized) -> Thử Opaque Refresh để khôi phục
    if (sessionRes.status === 401) {
      console.log("[Middleware Proxy] Session check returned 401, proxying Opaque Refresh");
      const refreshRes = await fetch(`${internalBaseUrl}/api/v1/auth/refresh`, {
        method: "POST",
        headers: {
          "Cookie": cookieHeader,
          "Host": "cloud.aurora.local",
          "Origin": clientOrigin,
        },
      });

      if (refreshRes.ok) {
        const response = NextResponse.next();
        const setCookies = refreshRes.headers.getSetCookie();
        if (setCookies.length > 0) {
          setCookies.forEach((cookieStr) => {
            response.headers.append("Set-Cookie", cookieStr);
          });
        } else {
          const setCookie = refreshRes.headers.get("Set-Cookie");
          if (setCookie) {
            response.headers.set("Set-Cookie", setCookie);
          }
        }
        console.log("[Middleware Proxy] Opaque Refresh proxy succeeded");
        return response;
      }
    }

    // [COMMENT]: Cả hai phương án refresh/check đều bị backend từ chối -> Đăng xuất và đẩy về login
    return redirectToSignIn();
  } catch (err) {
    console.error("[Middleware Proxy Error]: Failed to connect to controlplane:", err);
    // Áp dụng cơ chế Fail-Closed của môi trường HA để đảm bảo an toàn bảo mật
    return redirectToSignIn();
  }
}

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico|public/).*)"],
};
