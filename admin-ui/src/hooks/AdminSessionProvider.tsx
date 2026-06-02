/**
 * AdminSessionProvider.tsx — React Context Provider cho Admin Session.
 *
 * File này chỉ export 1 component duy nhất (AdminSessionProvider) để tuân thủ
 * quy tắc React Fast Refresh: một file chỉ nên export components.
 *
 * Kiến trúc session management:
 *
 *   admin-session-context.ts   ← types + context object + useAdminSessionContext
 *         ↑
 *   AdminSessionProvider.tsx   ← Provider component (file này)
 *         ↑
 *   App.tsx                    ← wrap root với <AdminSessionProvider>
 *
 *   useAdminSession.tsx        ← public hook + re-export types
 *
 * Cross-tab session sharing:
 *   cachedState và activeResolvePromise là module-level variables (singleton).
 *   Điều này đảm bảo nhiều instance của Provider (nếu có) dùng chung
 *   session state đã resolve, tránh gọi API trùng lặp.
 */

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'

import { AdminUnauthorizedError, getAdminSession, type AdminSession } from '@/lib/admin-session'
import { subscribeAdminUnauthorized, subscribeAdminSessionRefresh } from '@/lib/admin-auth-events'
import {
  AdminSessionContext,
  type AdminSessionState,
  type AdminSessionContextValue,
} from '@/hooks/admin-session-context'

// ---------------------------------------------------------------------------
// Constants & module-level session cache
// ---------------------------------------------------------------------------

const initialState: AdminSessionState = {
  loading: true,
  authenticated: false,
  session: null,
  error: '',
  notice: '',
}

const unauthenticatedState: AdminSessionState = {
  loading: false,
  authenticated: false,
  session: null,
  error: '',
  notice: '',
}

/** Timeout để tránh trường hợp network treo mãi không resolve */
const sessionLoadingTimeoutMs = 8000

/**
 * Cache session state ở module level để chia sẻ giữa các lần mount.
 * Khi user navigate giữa các trang, Provider unmount/remount nhưng
 * không cần gọi lại API nếu cachedState đã có.
 */
let cachedState: AdminSessionState | null = null

/**
 * Singleton promise đang resolve session.
 * Tránh race condition khi nhiều component mount cùng lúc cùng gọi API.
 */
let activeResolvePromise: Promise<AdminSessionState> | null = null

// ---------------------------------------------------------------------------
// State builders
// ---------------------------------------------------------------------------

function buildAuthenticatedState(session: AdminSession): AdminSessionState {
  return {
    loading: false,
    authenticated: true,
    session,
    error: '',
    notice: '',
  }
}

function buildUnauthenticatedState(overrides?: Partial<AdminSessionState>): AdminSessionState {
  return { ...unauthenticatedState, ...overrides }
}

// ---------------------------------------------------------------------------
// Session resolver (singleton async operation)
// ---------------------------------------------------------------------------

/**
 * Gọi API /admin/auth/session để verify session hiện tại.
 *
 * Deduplication: nếu đang có promise resolve, trả về promise đó luôn
 * (nhiều component mount cùng lúc chỉ tạo 1 request duy nhất).
 *
 * Retry policy: tối đa 2 lần retry (tổng 3 attempts) với delay 1.5s,
 * chỉ retry lỗi network — không retry AdminUnauthorizedError (401).
 */
function resolveAdminSession(): Promise<AdminSessionState> {
  if (activeResolvePromise) return activeResolvePromise

  activeResolvePromise = (async () => {
    let retryCount = 0

    while (true) {
      try {
        const session = await getAdminSession()
        const nextState = buildAuthenticatedState(session)
        cachedState = nextState
        return nextState
      } catch (error) {
        // 401 → session không hợp lệ, không retry
        if (error instanceof AdminUnauthorizedError) {
          cachedState = unauthenticatedState
          return unauthenticatedState
        }

        // Network error → retry tối đa 2 lần
        if (retryCount < 2) {
          retryCount += 1
          await new Promise((resolve) => window.setTimeout(resolve, 1500))
          continue
        }

        // Hết retry → trả về error state để UI hiển thị thông báo
        const failedState: AdminSessionState = buildUnauthenticatedState({
          error: error instanceof Error ? error.message : 'Cannot verify admin session.',
        })
        cachedState = failedState
        return failedState
      }
    }
  })()

  // Cleanup promise reference sau khi resolve/reject để cho phép retry sau này
  activeResolvePromise.finally(() => {
    activeResolvePromise = null
  })

  return activeResolvePromise
}

// ---------------------------------------------------------------------------
// Provider component (only export in this file)
// ---------------------------------------------------------------------------

/**
 * AdminSessionProvider — Provider duy nhất cần được đặt ở root của app.
 *
 * Nên đặt ở mức cao nhất (App.tsx) để:
 *   1. Context available cho mọi route
 *   2. Chỉ resolve session 1 lần khi app load
 *   3. Session state persist qua navigation (SPA)
 */
export function AdminSessionProvider({ children }: { children: ReactNode }) {
  /**
   * Lazy initializer: dùng function để React chỉ evaluate 1 lần tại mount.
   * Nếu cachedState đã có (e.g. Provider remount sau navigation), UI render
   * ngay với state đúng — không cần setState trong effect.
   *
   * Tại sao lazy function `() => ...` thay vì `cachedState ?? initialState`:
   *   Cả hai đều capture value tại mount-time, nhưng function form rõ ràng hơn
   *   về intent và tránh tạo object thừa khi React đang trong strict mode.
   */
  const [state, setState] = useState<AdminSessionState>(() => cachedState ?? initialState)
  const mountedRef = useRef(true)

  // ---------------------------------------------------------------------------
  // Effect 1: Subscribe sự kiện unauthorized (401 không recover được)
  // Khi fetch.ts emit AdminUnauthorized → logout + hiển thị notice "session_expired"
  // ---------------------------------------------------------------------------
  useEffect(() => {
    return subscribeAdminUnauthorized(() => {
      const nextState = buildUnauthenticatedState({ notice: 'session_expired' })
      cachedState = nextState
      if (mountedRef.current) setState(nextState)
    })
  }, [])

  // ---------------------------------------------------------------------------
  // Effect 2: Subscribe sự kiện session refresh thành công (silent refresh)
  // Khi fetch.ts tự refresh token ngầm → cập nhật UI mà không gọi lại /session
  // ---------------------------------------------------------------------------
  useEffect(() => {
    return subscribeAdminSessionRefresh(() => {
      // Ưu tiên dùng session đã có trong cachedState để giữ accessKey và các field khác.
      // Nếu cachedState chưa có hoặc đang loading → dùng session minimal.
      // Không gọi lại API /admin/auth/session để tránh request thừa.
      const existingSession = cachedState?.session ?? { authenticated: true as const }
      const nextState = buildAuthenticatedState(existingSession)
      cachedState = nextState
      if (mountedRef.current) setState(nextState)
    })
  }, [])

  // ---------------------------------------------------------------------------
  // Effect 3: Resolve session lần đầu khi app load
  //
  // Không setState synchronously trong effect body để tránh cascading render.
  // State khởi đầu đã được set đúng qua lazy initializer ở useState() phía trên.
  // ---------------------------------------------------------------------------
  useEffect(() => {
    mountedRef.current = true

    // Nếu cachedState đã resolve (từ lần mount trước), không làm gì thêm.
    // State đã được initialize đúng qua lazy initializer ở useState().
    if (cachedState && !cachedState.loading) {
      return () => {
        mountedRef.current = false
      }
    }

    // Đặt timeout để tránh user bị kẹt ở loading screen mãi mãi
    const timeoutID = window.setTimeout(() => {
      const timeoutState = buildUnauthenticatedState({
        error: 'Session check timeout. Please sign in again.',
      })
      cachedState = timeoutState
      if (mountedRef.current) setState(timeoutState)
    }, sessionLoadingTimeoutMs)

    // Resolve session async — setState chỉ được gọi trong callback (.then/.catch)
    // không phải synchronously trong body effect, tránh cascading render.
    void resolveAdminSession()
      .then((nextState) => {
        window.clearTimeout(timeoutID)
        if (mountedRef.current) setState(nextState)
      })
      .catch((error) => {
        window.clearTimeout(timeoutID)
        if (mountedRef.current) {
          setState(
            buildUnauthenticatedState({
              error: error instanceof Error ? error.message : 'Cannot verify admin session.',
            }),
          )
        }
      })

    return () => {
      window.clearTimeout(timeoutID)
      mountedRef.current = false
    }
  }, [])

  // ---------------------------------------------------------------------------
  // Callback actions (stable references — không thay đổi giữa các render)
  // ---------------------------------------------------------------------------

  /**
   * Refresh session thủ công (e.g. sau khi login, hoặc user click "Retry").
   * Trả về AdminSessionState mới để caller có thể react ngay.
   */
  const refreshSession = useCallback(async () => {
    if (mountedRef.current) {
      setState((current) => ({ ...current, loading: true, error: '', notice: '' }))
    }

    try {
      const nextState = await resolveAdminSession()
      if (mountedRef.current) setState(nextState)
      return nextState
    } catch (error) {
      const failedState = buildUnauthenticatedState({
        error: error instanceof Error ? error.message : 'Cannot verify admin session.',
      })
      cachedState = failedState
      if (mountedRef.current) setState(failedState)
      return failedState
    }
  }, [])

  /**
   * Set session authenticated sau khi login thành công mà không cần gọi lại API.
   * Dùng sau `/admin/auth/login` thành công.
   */
  const setAuthenticatedSession = useCallback((session: AdminSession) => {
    const nextState = buildAuthenticatedState(session)
    cachedState = nextState
    if (mountedRef.current) setState(nextState)
  }, [])

  /**
   * Clear session (logout phía client). Không gọi API logout.
   * Dùng sau khi `/admin/auth/logout` thành công.
   */
  const clearSession = useCallback(() => {
    cachedState = unauthenticatedState
    if (mountedRef.current) setState(unauthenticatedState)
  }, [])

  /**
   * Consume notice (session_expired) sau khi đã hiển thị toast cho user.
   * Xóa notice khỏi state để tránh hiển thị lại khi component remount.
   */
  const consumeNotice = useCallback(() => {
    cachedState = {
      ...(cachedState ?? unauthenticatedState),
      notice: '',
    }
    if (mountedRef.current) {
      setState((current) => ({ ...current, notice: '' }))
    }
  }, [])

  // Memoize context value để tránh re-render không cần thiết cho consumer
  const value = useMemo<AdminSessionContextValue>(
    () => ({
      ...state,
      refreshSession,
      setAuthenticatedSession,
      clearSession,
      consumeNotice,
    }),
    [clearSession, consumeNotice, refreshSession, setAuthenticatedSession, state],
  )

  return <AdminSessionContext.Provider value={value}>{children}</AdminSessionContext.Provider>
}
