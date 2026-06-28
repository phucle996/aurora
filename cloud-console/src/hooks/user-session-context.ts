"use client";

/**
 * user-session-context.ts — Internal context definition cho User Session.
 *
 * File này chứa:
 *   - Các kiểu dữ liệu public: UserSessionNotice, UserSessionState, UserSessionContextValue
 *   - Đối tượng React context: UserSessionContext
 *   - Hook nội bộ: useUserSessionContext (sử dụng bởi useUserSession)
 *
 * Tách biệt cấu trúc để hỗ trợ React Fast Refresh hoạt động ổn định.
 */

import { createContext, useContext } from "react";
import type { UserSession } from "@/lib/api/session";

// [COMMENT]: Định nghĩa notice để hiển thị một lần duy nhất khi phiên hết hạn
export type UserSessionNotice = "session_expired" | "";

// [COMMENT]: Khai báo state lưu trữ thông tin phiên làm việc hiện tại
export type UserSessionState = {
  loading: boolean;
  authenticated: boolean;
  session: UserSession | null;
  error: string;
  notice: UserSessionNotice;
};

// [COMMENT]: Khai báo các hành động (actions) có sẵn trong context
export type UserSessionContextValue = UserSessionState & {
  // [COMMENT]: Gọi API kiểm tra lại trạng thái phiên từ server
  refreshSession: () => Promise<UserSessionState>;
  // [COMMENT]: Thiết lập trạng thái đăng nhập nhanh không cần gọi lại API (sau khi login thành công)
  setAuthenticatedSession: (session: UserSession) => void;
  // [COMMENT]: Xoá thông tin phiên làm việc phía client (logout)
  clearSession: () => void;
  // [COMMENT]: Xoá thông báo one-shot sau khi đã hiển thị toast xong
  consumeNotice: () => void;
};

// [COMMENT]: Khởi tạo React Context cho User Session
export const UserSessionContext = createContext<UserSessionContextValue | null>(null);

// [COMMENT]: Hook nội bộ tiêu dùng context, ném lỗi nếu gọi ngoài Provider
export function useUserSessionContext(): UserSessionContextValue {
  const context = useContext(UserSessionContext);
  if (!context) {
    throw new Error("useUserSession must be used within UserSessionProvider");
  }
  return context;
}
