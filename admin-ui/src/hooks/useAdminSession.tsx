/**
 * useAdminSession.tsx — Public hook cho Admin Session context.
 *
 * File này chỉ export hook + re-export types để tuân thủ React Fast Refresh.
 * Types và context object nằm ở: hooks/admin-session-context.ts
 * Provider component nằm ở: hooks/AdminSessionProvider.tsx
 *
 * Import pattern cho consumer:
 *   import { useAdminSession } from '@/hooks/useAdminSession'
 *   import type { AdminSessionState } from '@/hooks/useAdminSession'
 */

import { useAdminSessionContext } from '@/hooks/admin-session-context'

// Re-export types để consumer không cần import từ internal context file
export type { AdminSessionNotice, AdminSessionState, AdminSessionContextValue } from '@/hooks/admin-session-context'

// ---------------------------------------------------------------------------
// Public hook
// ---------------------------------------------------------------------------

/**
 * Hook để tiêu thụ Admin Session context.
 *
 * Phải được dùng bên trong <AdminSessionProvider>.
 * Throws nếu không có Provider trong cây component.
 *
 * @example
 *   const { authenticated, session, loading } = useAdminSession()
 *   const { refreshSession, clearSession, consumeNotice } = useAdminSession()
 */
export function useAdminSession() {
  return useAdminSessionContext()
}
