/**
 * useUserSession.ts — Hook công khai để tiêu thụ User Session context.
 *
 * Cho phép các component con dễ dàng truy cập trạng thái đăng nhập,
 * thông tin phiên làm việc, và các action tương tác như refresh hay logout.
 */

import { useUserSessionContext } from "@/hooks/user-session-context";

// [COMMENT]: Re-export các types để các component sử dụng tiện lợi mà không cần import trực tiếp từ context
export type { UserSessionNotice, UserSessionState, UserSessionContextValue } from "@/hooks/user-session-context";

// [COMMENT]: Hook tiêu dùng phiên đăng nhập của người dùng thường
export function useUserSession() {
  return useUserSessionContext();
}
