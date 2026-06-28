"use client";

import { useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { Toaster } from "@/components/ui/sonner";

// [COMMENT]: Import shadcn components — enterprise-grade UI primitives
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

// [COMMENT]: Import API layers từ codebase Control Plane
import { authAPI } from "@/lib/api/auth";

// [COMMENT]: Icon SVG nhỏ gọn cho Logo thương hiệu
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

export default function SignUpPage() {
  const router = useRouter();

  // [COMMENT]: Quản lý các trường dữ liệu đăng ký tài khoản mới
  const [username, setUsername] = useState("");
  const [fullname, setFullname] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [rePassword, setRePassword] = useState("");

  const [isLoading, setIsLoading] = useState(false);

  // [COMMENT]: Hàm xử lý logic đăng ký tài khoản và validate bằng toast thông báo
  const handleSignUp = useCallback(async (e: React.FormEvent) => {
    e.preventDefault();

    // [COMMENT]: Vô hiệu hóa thông báo lỗi HTML mặc định của trình duyệt để hiển thị bằng Sonner Toast
    if (!username.trim()) {
      toast.error("Username is required.");
      return;
    }
    if (!fullname.trim()) {
      toast.error("Full name is required.");
      return;
    }
    if (!email.trim()) {
      toast.error("Email address is required.");
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
    if (password !== rePassword) {
      toast.error("Passwords do not match.");
      return;
    }

    setIsLoading(true);

    try {
      // [COMMENT]: Gọi API đăng ký tài khoản của auth module
      await authAPI.register({
        username,
        fullname,
        email,
        password,
        re_password: rePassword
      });

      toast.success("Account created successfully! Redirecting to sign in page...");
      
      // [COMMENT]: Chuyển hướng về trang đăng nhập sau khi đăng ký thành công
      setTimeout(() => {
        router.push("/signin");
      }, 1500);
    } catch (err: unknown) {
      const apiError = err as { status?: number; message?: string };
      const msg = apiError?.message || "Registration failed. Please try again.";
      toast.error(msg);
    } finally {
      setIsLoading(false);
    }
  }, [username, fullname, email, password, rePassword, router]);

  return (
    <div className="flex min-h-screen flex-col bg-[#F8FAFC]">
      {/* Main content — Form đăng ký chính ở giữa màn hình */}
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
          {/* REGISTER CARD — Enterprise style, clean, matching signin layout   */}
          {/* ================================================================= */}
          <div className="rounded-xl border border-[#E5E7EB] bg-white shadow-none">
            <div className="p-6 space-y-5">
              <div className="space-y-1">
                <h2 className="text-base font-medium text-foreground">Create your account</h2>
                <p className="text-sm text-muted-foreground">
                  Access enterprise infrastructure workspaces.
                </p>
              </div>

              {/* [COMMENT]: Form đăng ký hỗ trợ validate tùy chỉnh và tắt mặc định browser validation */}
              <form onSubmit={handleSignUp} className="space-y-4" id="signup-form" noValidate>
                
                {/* Username Input */}
                <div className="space-y-2">
                  <Label htmlFor="username" className="text-sm font-normal text-foreground">
                    Username
                  </Label>
                  <Input
                    id="username"
                    type="text"
                    placeholder="Choose a username"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    disabled={isLoading}
                    className="h-9 rounded-[8px]"
                  />
                </div>

                {/* Full Name Input */}
                <div className="space-y-2">
                  <Label htmlFor="fullname" className="text-sm font-normal text-foreground">
                    Full Name
                  </Label>
                  <Input
                    id="fullname"
                    type="text"
                    placeholder="Enter your full name"
                    value={fullname}
                    onChange={(e) => setFullname(e.target.value)}
                    disabled={isLoading}
                    className="h-9 rounded-[8px]"
                  />
                </div>

                {/* Email Input */}
                <div className="space-y-2">
                  <Label htmlFor="email" className="text-sm font-normal text-foreground">
                    Email Address
                  </Label>
                  <Input
                    id="email"
                    type="email"
                    placeholder="name@company.com"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    disabled={isLoading}
                    className="h-9 rounded-[8px]"
                  />
                </div>

                {/* Password Input */}
                <div className="space-y-2">
                  <Label htmlFor="password" className="text-sm font-normal text-foreground">
                    Password
                  </Label>
                  <Input
                    id="password"
                    type="password"
                    placeholder="At least 8 characters"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    disabled={isLoading}
                    className="h-9 rounded-[8px]"
                  />
                </div>

                {/* Confirm Password Input */}
                <div className="space-y-2">
                  <Label htmlFor="re_password" className="text-sm font-normal text-foreground">
                    Confirm Password
                  </Label>
                  <Input
                    id="re_password"
                    type="password"
                    placeholder="Repeat your password"
                    value={rePassword}
                    onChange={(e) => setRePassword(e.target.value)}
                    disabled={isLoading}
                    className="h-9 rounded-[8px]"
                  />
                </div>

                <Button
                  type="submit"
                  className="w-full h-9 bg-[#2563EB] hover:bg-[#1D4ED8] text-white rounded-[8px] mt-2 cursor-pointer"
                  disabled={isLoading}
                  id="signup-button"
                >
                  {isLoading ? (
                    <span className="flex items-center gap-2">
                      <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/30 border-t-white" />
                      Creating account…
                    </span>
                  ) : (
                    "Register Account"
                  )}
                </Button>
              </form>

              <div className="h-px bg-slate-100 dark:bg-slate-800 my-1" />

              {/* Link quay lại trang đăng nhập */}
              <div className="text-center text-xs text-muted-foreground pt-1 select-none">
                Already have an account?{" "}
                <a
                  href="/signin"
                  className="font-medium text-[#2563EB] hover:text-[#1D4ED8] transition-colors"
                >
                  Sign in
                </a>
              </div>

            </div>
          </div>

          {/* ================================================================= */}
          {/* LINKS — Documentation, Support, Privacy, Terms                     */}
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

      {/* [COMMENT]: Khối Toaster để nhận thông báo lỗi đăng ký trực tiếp từ server */}
      <Toaster />
    </div>
  );
}
