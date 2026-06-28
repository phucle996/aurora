"use client";

import React, { useEffect } from "react";
import { useRouter } from "next/navigation";

export default function SignUpPage() {
  const router = useRouter();

  // [COMMENT]: Chuyển hướng người dùng về trang đăng nhập hợp nhất với chế độ signup để chạy animation
  useEffect(() => {
    router.replace("/signin?mode=signup");
  }, [router]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-[#F8FAFC] dark:bg-[#0B0F19]">
      <div className="h-8 w-8 animate-spin rounded-full border-2 border-[#2563EB]/30 border-t-[#2563EB]" />
    </div>
  );
}
