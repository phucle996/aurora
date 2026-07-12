// [COMMENT]: Sử dụng relative path "" trên client-side để request đi cùng origin (same-origin),
// giúp trình duyệt tự động gửi cookie mà không bị chặn bởi chính sách CORS.
// Trên server-side (Next.js SSR), sử dụng địa chỉ Envoy cấu hình trực tiếp từ biến môi trường (không dùng fallback).
export const controlplaneBaseURL =
  typeof window !== "undefined"
    ? ""
    : (process.env.NEXT_PUBLIC_ENVOY_URL?.trim() || "");

