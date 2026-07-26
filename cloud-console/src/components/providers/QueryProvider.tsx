"use client";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import React, { useState } from "react";
import { isAPIError } from "@/shared/api/http";

// [COMMENT]: QueryProvider bọc các children bằng QueryClientProvider của TanStack Query.
// Sử dụng useState để đảm bảo QueryClient chỉ được tạo một lần duy nhất ở Client-side (singleton),
// tránh việc recreate lại client trên mỗi lượt render của Next.js SSR.
export default function QueryProvider({ children }: { children: React.ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 30 * 1000,
            gcTime: 5 * 60 * 1000,
            refetchOnWindowFocus: true,
            refetchOnReconnect: true,
            retry: (failureCount, error) => isAPIError(error) && error.retryable && failureCount < 2,
            retryDelay: (attempt) => Math.min(5_000, 300 * 2 ** attempt + Math.round(Math.random() * 200)),
          },
          mutations: {
            // Mutations remain single-shot unless a feature supplies an explicit idempotency contract.
            retry: false,
          },
        },
      })
  );

  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}
