"use client";

import React, { useEffect, useState } from "react";
import { useRouter, usePathname } from "next/navigation";
import { ShieldAlert, ArrowLeft, Loader2 } from "lucide-react";
import { useUserSession } from "@/hooks/useUserSession";
import { cn } from "@/lib/utils";

// [COMMENT]: AdminRouteGuard bảo vệ các route nhạy cảm (/admin/*) trước các hành vi truy cập trái phép
interface AdminRouteGuardProps {
  children: React.ReactNode;
}

export default function AdminRouteGuard({ children }: AdminRouteGuardProps) {
  const router = useRouter();
  const pathname = usePathname();
  const { renderContext, loading, authenticated, checkPermission } = useUserSession();
  const [authorized, setAuthorized] = useState<boolean | null>(null);

  useEffect(() => {
    if (loading) return;

    // [COMMENT]: Chặn đứng nếu người dùng chưa thực hiện đăng nhập
    if (!authenticated) {
      router.push("/signin");
      return;
    }

    // [COMMENT]: Xác định requiredKey tương ứng với admin path hiện tại
    let requiredKey = "";
    if (pathname.startsWith("/admin/users")) {
      requiredKey = "*:*:iam:users";
    } else if (pathname.startsWith("/admin/rbac")) {
      requiredKey = "*:*:iam:rbac";
    }

    let allowed = false;
    if (requiredKey) {
      // User cần có quyền đọc (read / list / *) đối với domain đó
      allowed = checkPermission(requiredKey, "read") || 
                checkPermission(requiredKey, "list") || 
                checkPermission(requiredKey, "*");
    } else {
      allowed = true;
    }

    setAuthorized(allowed);
  }, [loading, authenticated, renderContext, pathname, router]);

  // [COMMENT]: Render màn hình Loading chuyển tiếp cực kỳ mượt mà
  if (loading || authorized === null) {
    return (
      <div className="flex h-screen w-full flex-col items-center justify-center bg-slate-900 text-slate-100">
        <Loader2 className="h-8 w-8 animate-spin text-blue-500" />
        <p className="mt-4 text-xs font-semibold text-slate-400 uppercase tracking-widest animate-pulse">
          Verifying security clearance...
        </p>
      </div>
    );
  }

  // [COMMENT]: Render trang lỗi 403 Forbidden với thiết kế cao cấp, huyền bí khi bị chặn quyền truy cập
  if (!authorized) {
    return (
      <div className="flex h-screen w-full flex-col items-center justify-center bg-[#0B0F19] px-6 text-center select-none text-slate-200">
        {/* Glow Effect */}
        <div className="absolute top-1/3 left-1/2 -translate-x-1/2 -translate-y-1/2 w-80 h-80 rounded-full bg-red-500/10 blur-[100px] pointer-events-none" />

        <div className="relative flex h-16 w-16 items-center justify-center rounded-2xl bg-red-500/10 border border-red-500/20 text-red-500 mb-6 shadow-[0_0_20px_rgba(239,68,68,0.07)] animate-bounce">
          <ShieldAlert className="h-8 w-8" />
        </div>

        <h1 className="text-xl font-bold tracking-tight text-slate-100 sm:text-2xl">
          Access Denied
        </h1>
        <p className="mt-3 max-w-sm text-sm text-slate-400 leading-relaxed">
          Your account level does not hold sufficient cryptographic clearance (permissions) to access this administrative portal.
        </p>

        <button
          onClick={() => router.push("/")}
          className="mt-8 flex items-center gap-2 rounded-xl bg-slate-900 border border-slate-800 hover:border-slate-700/80 px-4 py-2 text-xs font-semibold text-slate-300 hover:text-white transition-all cursor-pointer shadow-lg"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          <span>Back to Dashboard</span>
        </button>
      </div>
    );
  }

  // [COMMENT]: User hợp lệ, trả về layout trang con
  return <>{children}</>;
}
