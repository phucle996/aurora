"use client";

import { useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { Toaster } from "@/components/ui/sonner";

// [COMMENT]: Import shadcn components — enterprise-grade UI primitives
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Separator } from "@/components/ui/separator";

// [COMMENT]: Import các API và security modules đã port từ codebase cũ
import { authAPI, type LoginRequest } from "@/lib/api/auth";
import { ensureDevicePublicKey, DeviceKeyUnsupportedError } from "@/lib/security/deviceKey";

// [COMMENT]: Icon SVG nhỏ gọn — tránh import thư viện icon nặng cho trang login
function AuroraLogo() {
  return (
    <svg width="32" height="32" viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
      <rect width="32" height="32" rx="8" fill="#2563EB" />
      <path
        d="M16 7L23 23H9L16 7Z"
        fill="white"
        stroke="white"
        strokeWidth="1.5"
        strokeLinejoin="round"
      />
    </svg>
  );
}

// [COMMENT]: Icon cho các SSO providers — inline SVG để tránh external dependency
function MicrosoftIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <rect x="1" y="1" width="6.5" height="6.5" fill="#F25022" />
      <rect x="8.5" y="1" width="6.5" height="6.5" fill="#7FBA00" />
      <rect x="1" y="8.5" width="6.5" height="6.5" fill="#00A4EF" />
      <rect x="8.5" y="8.5" width="6.5" height="6.5" fill="#FFB900" />
    </svg>
  );
}

function GoogleIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <path d="M15.68 8.18c0-.57-.05-1.12-.15-1.64H8v3.1h4.31a3.68 3.68 0 01-1.6 2.42v2h2.59c1.51-1.39 2.38-3.44 2.38-5.88z" fill="#4285F4" />
      <path d="M8 16c2.16 0 3.97-.72 5.3-1.94l-2.59-2a4.77 4.77 0 01-7.13-2.51H.96v2.06A8 8 0 008 16z" fill="#34A853" />
      <path d="M3.58 9.55a4.8 4.8 0 010-3.1V4.39H.96a8 8 0 000 7.22l2.62-2.06z" fill="#FBBC05" />
      <path d="M8 3.16a4.34 4.34 0 013.07 1.2l2.3-2.3A7.72 7.72 0 008 0 8 8 0 00.96 4.39l2.62 2.06A4.77 4.77 0 018 3.16z" fill="#EA4335" />
    </svg>
  );
}

function GitHubIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

/* [COMMENT]: PlatformStatus component đã được di chuyển trực tiếp xuống đáy màn hình dưới dạng footer toàn trang */

export default function SignInPage() {
  const router = useRouter();

  // [COMMENT]: State quản lý form input
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [trustDevice, setTrustDevice] = useState(false);

  // [COMMENT]: State quản lý trạng thái UI
  const [isLoading, setIsLoading] = useState(false);

  // [COMMENT]: Hàm xử lý đăng nhập chính — sinh device key, gọi API, xử lý lỗi
  const handleSignIn = useCallback(async (e: React.FormEvent) => {
    e.preventDefault();

    // [COMMENT]: Validate thủ công bằng React và hiển thị Sonner Toast thay vì thông báo mặc định của trình duyệt
    if (!username.trim()) {
      toast.error("Username is required.");
      return;
    }
    if (!password.trim()) {
      toast.error("Password is required.");
      return;
    }
    if (password.length < 8) {
      toast.error("Password must be at least 8 characters.");
      return;
    }

    setIsLoading(true);

    try {
      // [COMMENT]: Bước 1 — Sinh hoặc đọc lại cặp khóa Ed25519 của thiết bị từ IndexedDB.
      // Đây là yêu cầu bắt buộc của Controlplane để xác thực device binding.
      let devicePublicKey = "";
      try {
        devicePublicKey = await ensureDevicePublicKey();
      } catch (err) {
        if (err instanceof DeviceKeyUnsupportedError) {
          // [COMMENT]: Trình duyệt không hỗ trợ Ed25519 Web Crypto — vẫn tiếp tục nhưng
          // device binding sẽ bị vô hiệu hoá phía server.
          console.warn("[SignIn] Ed25519 not supported, proceeding without device key");
        } else {
          throw err;
        }
      }

      // [COMMENT]: Bước 2 — Gọi API login với payload chuẩn của Controlplane.
      // zone_code để trống — server sẽ tự chọn default zone cho user.
      const payload: LoginRequest = {
        username,
        password,
        device_public_key: devicePublicKey,
        trust_device: trustDevice,
        zone_code: "",
      };

      await authAPI.login(payload);

      // [COMMENT]: Bước 3 — Đăng nhập thành công → chuyển hướng tới trang chính
      router.push("/");
    } catch (err: unknown) {
      // [COMMENT]: Phân tích lỗi từ API layer — APIError có dạng { status, message }
      // Hiển thị trực tiếp thông báo lỗi từ server, không wrap hay sửa đổi nội dung
      const apiError = err as { status?: number; message?: string };
      const msg = apiError?.message || "An unexpected error occurred. Please try again.";
      toast.error(msg);
    } finally {
      setIsLoading(false);
    }
  }, [username, password, trustDevice, router]);

  return (
    <div className="flex min-h-screen flex-col bg-[#F8FAFC]">
      {/* [COMMENT]: Main content — form đăng nhập ở giữa màn hình */}
      <div className="flex flex-1 items-center justify-center px-4 py-12">
        <div className="w-full max-w-[400px] space-y-6">

          {/* ================================================================= */}
          {/* HEADER — Logo + Branding — mang cảm giác Control Plane             */}
          {/* ================================================================= */}
          <div className="space-y-1">
            <div className="flex items-center gap-3">
              <AuroraLogo />
              <div>
                <h1 className="text-lg font-semibold text-foreground tracking-tight">
                  Aurora Cloud
                </h1>
                <p className="text-xs text-muted-foreground">
                  Cloud Control Plane
                </p>
              </div>
            </div>
          </div>

          {/* ================================================================= */}
          {/* SIGN IN CARD — Enterprise style, clean, no illustration            */}
          {/* ================================================================= */}
          <div className="rounded-xl border border-[#E5E7EB] bg-white shadow-none">
            <div className="p-6 space-y-5">
              {/* [COMMENT]: Tiêu đề và mô tả ngắn */}
              <div className="space-y-1">
                <h2 className="text-base font-medium text-foreground">Sign in</h2>
                <p className="text-sm text-muted-foreground">
                  Manage your infrastructure securely.
                </p>
              </div>

              {/* [COMMENT]: Hiển thị lỗi đăng nhập qua Toaster thay vì chèn div làm bể UI */}

              {/* [COMMENT]: Form đăng nhập — Username + Password + Remember me */}
              <form onSubmit={handleSignIn} className="space-y-4" id="signin-form" noValidate>
                <div className="space-y-2">
                  <Label htmlFor="username" className="text-sm font-normal text-foreground">
                    Username
                  </Label>
                  <Input
                    id="username"
                    type="text"
                    placeholder="Enter your username"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    autoComplete="username"
                    disabled={isLoading}
                    className="h-9 rounded-[8px]"
                  />
                </div>

                <div className="space-y-2">
                  <div className="flex items-center justify-between">
                    <Label htmlFor="password" className="text-sm font-normal text-foreground">
                      Password
                    </Label>
                    <a
                      href="#"
                      className="text-xs text-[#2563EB] hover:text-[#1D4ED8] transition-colors"
                    >
                      Forgot password?
                    </a>
                  </div>
                  <Input
                    id="password"
                    type="password"
                    placeholder="••••••••"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    autoComplete="current-password"
                    disabled={isLoading}
                    className="h-9 rounded-[8px]"
                  />
                </div>

                <div className="flex items-center gap-2">
                  <Checkbox
                    id="remember"
                    checked={trustDevice}
                    onCheckedChange={(checked) => setTrustDevice(checked === true)}
                    disabled={isLoading}
                  />
                  <Label htmlFor="remember" className="text-sm font-normal text-muted-foreground cursor-pointer select-none">
                    Trust device in 30 days
                  </Label>
                </div>

                <Button
                  type="submit"
                  className="w-full h-9 bg-[#2563EB] hover:bg-[#1D4ED8] text-white rounded-[8px]"
                  disabled={isLoading}
                  id="signin-button"
                >
                  {isLoading ? (
                    <span className="flex items-center gap-2">
                      <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/30 border-t-white" />
                      Signing in…
                    </span>
                  ) : (
                    "Sign In"
                  )}
                </Button>
              </form>

              {/* [COMMENT]: Separator giữa form login và SSO options */}
              <div className="relative">
                <Separator />
                <span className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-white px-3 text-xs text-muted-foreground">
                  Continue with SSO
                </span>
              </div>

              {/* ============================================================= */}
              {/* SSO BUTTONS — Enterprise priority: Microsoft → Google → GitHub */}
              {/* ============================================================= */}
              <div className="grid grid-cols-3 gap-2">
                <Button
                  variant="outline"
                  className="h-9 text-xs font-normal gap-1.5 rounded-[8px]"
                  disabled={isLoading}
                  id="sso-microsoft"
                >
                  <MicrosoftIcon />
                  Microsoft
                </Button>
                <Button
                  variant="outline"
                  className="h-9 text-xs font-normal gap-1.5 rounded-[8px]"
                  disabled={isLoading}
                  id="sso-google"
                >
                  <GoogleIcon />
                  Google
                </Button>
                <Button
                  variant="outline"
                  className="h-9 text-xs font-normal gap-1.5 rounded-[8px]"
                  disabled={isLoading}
                  id="sso-github"
                >
                  <GitHubIcon />
                  GitHub
                </Button>
              </div>

              {/* [COMMENT]: Link đăng ký tài khoản mới bằng tiếng Anh */}
              <div className="text-center text-xs text-muted-foreground pt-1.5 select-none">
                Don't have an account?{" "}
                <a
                  href="/signup"
                  className="font-medium text-[#2563EB] hover:text-[#1D4ED8] transition-colors"
                >
                  Sign up
                </a>
              </div>
            </div>
          </div>

          {/* ================================================================= */}
          {/* LINKS — Documentation, Support, Privacy, Terms trên cùng 1 hàng   */}
          {/* ================================================================= */}
          <div className="flex items-center justify-center gap-3 text-xs text-muted-foreground font-medium select-none">
            <a href="#" className="hover:text-foreground transition-colors">Documentation</a>
            <span>·</span>
            <a href="#" className="hover:text-foreground transition-colors">Support</a>
            <span>·</span>
            <a href="#" className="hover:text-foreground transition-colors">Privacy</a>
            <span>·</span>
            <a href="#" className="hover:text-foreground transition-colors">Terms</a>
          </div>
        </div>
      </div>
      {/* [COMMENT]: Khối hiển thị trạng thái hệ thống và phiên bản được di chuyển xuống đáy màn hình */}
      <div className="w-full py-4 border-t border-[#E5E7EB] bg-white text-xs text-muted-foreground flex items-center justify-center gap-4 mt-auto select-none">
        <span className="flex items-center gap-1.5 font-semibold text-emerald-600">
          <span className="relative flex h-2 w-2">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
            <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-500" />
          </span>
          All Systems Operational
        </span>
        <span className="text-slate-300">|</span>
        <span className="font-mono text-slate-500">v1.0.0</span>
      </div>
      {/* [COMMENT]: Khối Toaster để nhận thông báo lỗi đăng nhập trực tiếp từ server */}
      <Toaster />
    </div>
  );
}
