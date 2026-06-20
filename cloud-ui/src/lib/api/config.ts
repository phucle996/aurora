// [COMMENT]: Sử dụng relative path "" trên client-side (browser) để request đi qua cùng origin
// (ví dụ: https://cloud.aurora.local/api/v1/auth/login), giúp trình duyệt chấp nhận cookie domain "aurora.local".
// Trên server-side (Next.js SSR), giữ fallback là absolute URL để tránh lỗi fetch.
export const controlplaneBaseURL =
  typeof window !== "undefined"
    ? ""
    : (process.env.NEXT_PUBLIC_CONTROLPLANE_URL?.trim() || "http://localhost:28000");

