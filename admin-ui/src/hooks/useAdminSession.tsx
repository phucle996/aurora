import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'

import { AdminUnauthorizedError, getAdminSession, type AdminSession } from '@/lib/admin-session'
import { subscribeAdminUnauthorized, subscribeAdminSessionRefresh } from '@/lib/admin-auth-events'

export type AdminSessionNotice = 'session_expired' | ''

export type AdminSessionState = {
  loading: boolean
  authenticated: boolean
  session: AdminSession | null
  error: string
  notice: AdminSessionNotice
}

type AdminSessionContextValue = AdminSessionState & {
  refreshSession: () => Promise<AdminSessionState>
  setAuthenticatedSession: (session: AdminSession) => void
  clearSession: () => void
  consumeNotice: () => void
}

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

const sessionLoadingTimeoutMs = 8000

const AdminSessionContext = createContext<AdminSessionContextValue | null>(null)

let cachedState: AdminSessionState | null = null

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
  return {
    ...unauthenticatedState,
    ...overrides,
  }
}

async function resolveAdminSession(signal?: AbortSignal): Promise<AdminSessionState> {
  let retryCount = 0

  while (true) {
    try {
      const session = await getAdminSession(signal)
      const nextState = buildAuthenticatedState(session)
      cachedState = nextState
      return nextState
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') {
        throw error
      }

      if (error instanceof AdminUnauthorizedError) {
        cachedState = unauthenticatedState
        return unauthenticatedState
      }

      if (retryCount < 2) {
        retryCount += 1
        await new Promise((resolve) => window.setTimeout(resolve, 1500))
        continue
      }

      const failedState: AdminSessionState = buildUnauthenticatedState({
        error: error instanceof Error ? error.message : 'Cannot verify admin session.',
      })
      cachedState = failedState
      return failedState
    }
  }
}

export function AdminSessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AdminSessionState>(cachedState ?? initialState)
  const mountedRef = useRef(true)

  useEffect(() => {
    return subscribeAdminUnauthorized(() => {
      const nextState = buildUnauthenticatedState({ notice: 'session_expired' })
      cachedState = nextState
      if (mountedRef.current) {
        setState(nextState)
      }
    })
  }, [])

  useEffect(() => {
    return subscribeAdminSessionRefresh(() => {
      // Khi nhận được tín hiệu refresh session ngầm thành công, gọi resolveAdminSession ngầm để đồng bộ state
      resolveAdminSession()
        .then((nextState) => {
          if (mountedRef.current) {
            setState(nextState)
          }
        })
        .catch(() => {
          // Bỏ qua lỗi check session ngầm để tránh ảnh hưởng tiêu cực đến UX
        })
    })
  }, [])


  useEffect(() => {
    mountedRef.current = true
    if (cachedState && !cachedState.loading) {
      setState(cachedState)
      return () => {
        mountedRef.current = false
      }
    }

    const controller = new AbortController()
    const timeoutID = window.setTimeout(() => {
      controller.abort()
      const timeoutState = buildUnauthenticatedState({
        error: 'Session check timeout. Please sign in again.',
      })
      cachedState = timeoutState
      if (mountedRef.current) {
        setState(timeoutState)
      }
    }, sessionLoadingTimeoutMs)
    setState((current) => (current.loading ? current : initialState))

    void resolveAdminSession(controller.signal)
      .then((nextState) => {
        window.clearTimeout(timeoutID)
        if (mountedRef.current) {
          setState(nextState)
        }
      })
      .catch((error) => {
        window.clearTimeout(timeoutID)
        if (error instanceof DOMException && error.name === 'AbortError') {
          return
        }
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
      controller.abort()
    }
  }, [])

  const refreshSession = useCallback(async () => {
    const controller = new AbortController()
    if (mountedRef.current) {
      setState((current) => ({ ...current, loading: true, error: '', notice: '' }))
    }

    try {
      const timeoutID = window.setTimeout(() => {
        controller.abort()
      }, sessionLoadingTimeoutMs)
      const nextState = await resolveAdminSession(controller.signal)
      window.clearTimeout(timeoutID)
      if (mountedRef.current) {
        setState(nextState)
      }
      return nextState
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') {
        const timeoutState = buildUnauthenticatedState({
          error: 'Session check timeout. Please sign in again.',
        })
        cachedState = timeoutState
        if (mountedRef.current) {
          setState(timeoutState)
        }
        return timeoutState
      }
      const failedState = buildUnauthenticatedState({
        error: error instanceof Error ? error.message : 'Cannot verify admin session.',
      })
      cachedState = failedState
      if (mountedRef.current) {
        setState(failedState)
      }
      return failedState
    }
  }, [])

  const setAuthenticatedSession = useCallback((session: AdminSession) => {
    const nextState = buildAuthenticatedState(session)
    cachedState = nextState
    if (mountedRef.current) {
      setState(nextState)
    }
  }, [])

  const clearSession = useCallback(() => {
    cachedState = unauthenticatedState
    if (mountedRef.current) {
      setState(unauthenticatedState)
    }
  }, [])

  const consumeNotice = useCallback(() => {
    cachedState = {
      ...(cachedState ?? unauthenticatedState),
      notice: '',
    }
    if (mountedRef.current) {
      setState((current) => ({ ...current, notice: '' }))
    }
  }, [])

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

export function useAdminSession(): AdminSessionContextValue {
  const context = useContext(AdminSessionContext)
  if (!context) {
    throw new Error('useAdminSession must be used within AdminSessionProvider')
  }
  return context
}
