/**
 * admin-session-context.ts — Internal context definition cho Admin Session.
 *
 * File này chứa:
 *   - Public types: AdminSessionNotice, AdminSessionState, AdminSessionContextValue
 *   - React context object: AdminSessionContext
 *   - Internal hook: useAdminSessionContext (dùng bởi useAdminSession)
 *
 * Tách ra file riêng để tuân thủ React Fast Refresh:
 *   - AdminSessionProvider.tsx chỉ export component
 *   - useAdminSession.tsx chỉ export hook + re-export types
 *   - File này export context + types (không phải component) → Fast Refresh OK
 *     vì file này không export component nào, chỉ export non-component.
 *
 * Dependency graph (không circular):
 *   admin-session.ts (AdminSession type)
 *       ↑
 *   admin-session-context.ts (types + context + hook)
 *       ↑                   ↑
 *   AdminSessionProvider.tsx  useAdminSession.tsx
 */

import { createContext, useContext } from 'react'
import type { AdminSession } from '@/lib/admin-session'

// ---------------------------------------------------------------------------
// Types — public API, được re-export qua useAdminSession.tsx
// ---------------------------------------------------------------------------

/**
 * Notice được hiển thị 1 lần cho user, sau đó consumeNotice() xóa đi.
 * 'session_expired': session bị hết hạn và không tự refresh được.
 */
export type AdminSessionNotice = 'session_expired' | ''

/**
 * Toàn bộ state của admin session — được cung cấp qua context.
 */
export type AdminSessionState = {
  /** true khi đang resolve session lần đầu (chưa biết authenticated hay không) */
  loading: boolean
  /** true nếu session hợp lệ */
  authenticated: boolean
  /** thông tin session (accessKey, ...) — null khi chưa authenticated */
  session: AdminSession | null
  /** thông báo lỗi nếu resolve session thất bại */
  error: string
  /** thông báo one-shot (e.g. session_expired) */
  notice: AdminSessionNotice
}

/**
 * Toàn bộ giá trị context — AdminSessionState + các action methods.
 */
export type AdminSessionContextValue = AdminSessionState & {
  /** Gọi lại /admin/auth/session (dùng sau login, hoặc retry) */
  refreshSession: () => Promise<AdminSessionState>
  /** Set session authenticated mà không gọi API (dùng sau login flow) */
  setAuthenticatedSession: (session: AdminSession) => void
  /** Logout phía client (set unauthenticated) */
  clearSession: () => void
  /** Xóa notice sau khi đã hiển thị toast */
  consumeNotice: () => void
}

// ---------------------------------------------------------------------------
// Context object — internal, nhưng exported để AdminSessionProvider.tsx dùng
// ---------------------------------------------------------------------------

/**
 * React context object cho admin session.
 * Không dùng trực tiếp trong component — dùng qua useAdminSession() hook.
 */
export const AdminSessionContext = createContext<AdminSessionContextValue | null>(null)

// ---------------------------------------------------------------------------
// Internal hook — dùng bởi useAdminSession.tsx
// ---------------------------------------------------------------------------

/**
 * Internal hook để consume AdminSessionContext.
 * @internal Không dùng trực tiếp — dùng qua useAdminSession() từ useAdminSession.tsx.
 */
export function useAdminSessionContext(): AdminSessionContextValue {
  const context = useContext(AdminSessionContext)
  if (!context) {
    throw new Error('useAdminSession must be used within AdminSessionProvider')
  }
  return context
}
