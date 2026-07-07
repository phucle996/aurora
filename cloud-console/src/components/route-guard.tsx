"use client";

import React, { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { ShieldAlert, ArrowLeft, Loader2 } from "lucide-react";
import { useUserSession } from "@/hooks/useUserSession";

// [COMMENT]: RouteGuard kiểm tra quyền truy cập dựa trên key và action được cấu hình động cho mỗi route.
// Thiết kế thuần Render Engine, không chứa bất kỳ logic nghiệp vụ hoặc cứng hóa URL/Quyền hạn nào.
interface RouteGuardProps {
  children: React.ReactNode;
  requiredKey: string;
  requiredAction: string;
}

export default function RouteGuard({ children, requiredKey, requiredAction }: RouteGuardProps) {
  const router = useRouter();
  const { loading, authenticated, checkPermission } = useUserSession();
  const [authorized, setAuthorized] = useState<boolean | null>(null);

  useEffect(() => {
    if (loading) return;

    // [COMMENT]: Chặn đứng nếu người dùng chưa đăng nhập
    if (!authenticated) {
      router.push("/signin");
      return;
    }

    // [COMMENT]: So khớp quyền hạn động sử dụng triết lý pure render engine
    const allowed = checkPermission(requiredKey, requiredAction);
    setAuthorized(allowed);
  }, [loading, authenticated, requiredKey, requiredAction, checkPermission, router]);

  // [COMMENT]: Giao diện chờ kiểm tra quyền hạn mượt mà
  if (loading || authorized === null) {
    return (
      <div className="flex h-screen w-full flex-col items-center justify-center bg-slate-900 text-slate-100">
        <Loader2 className="h-8 w-8 animate-spin text-blue-500" />
        <p className="mt-4 text-xs font-semibold text-slate-400 uppercase tracking-widest animate-pulse">
          Verifying clearance...
        </p>
      </div>
    );
  }

  // [COMMENT]: Trang Access Denied 403 cao cấp khi không đủ quyền hạn
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
          Your account does not possess the required clearance key or action context to view this page.
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

  return <>{children}</>;
}
