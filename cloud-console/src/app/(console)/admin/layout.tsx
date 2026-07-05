"use client";

import React from "react";
import AdminRouteGuard from "@/components/admin-route-guard";

// [COMMENT]: AdminLayout bọc toàn bộ các route con /admin/* bằng AdminRouteGuard để đảm bảo an toàn tuyệt đối
export default function AdminLayout({ children }: { children: React.ReactNode }) {
  return <AdminRouteGuard>{children}</AdminRouteGuard>;
}
