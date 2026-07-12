"use client";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import React, { useState } from "react";

// [COMMENT]: QueryProvider bọc các children bằng QueryClientProvider của TanStack Query.
// Sử dụng useState để đảm bảo QueryClient chỉ được tạo một lần duy nhất ở Client-side (singleton),
// tránh việc recreate lại client trên mỗi lượt render của Next.js SSR.
export default function QueryProvider({ children }: { children: React.ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            // [COMMENT]: Dữ liệu được coi là fresh trong 60 giây (không fetch lại liên tục)
            staleTime: 60 * 1000,
            // [COMMENT]: Tắt tự động refetch khi focus lại vào tab để giảm tải request trong dev
            refetchOnWindowFocus: false,
            // [COMMENT]: Thử lại tối đa 1 lần nếu request bị lỗi mạng
            retry: 1,
          },
        },
      })
  );

  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}
