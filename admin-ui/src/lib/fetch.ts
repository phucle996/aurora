import { emitAdminUnauthorized, emitAdminSessionRefresh } from '@/lib/admin-auth-events'

const ControlplaneURL = (import.meta.env.VITE_CONTROLPLANE_API_BASE_URL ?? '').trim()

function normalizeBaseURL(baseURL: string): string {
  if (baseURL === '') {
    return ''
  }

  return baseURL.replace(/\/+$/, '')
}

function toAbsoluteURL(baseURL: string, path: string): string {
  const trimmedPath = path.trim()
  if (trimmedPath === '') {
    return normalizeBaseURL(baseURL)
  }

  if (/^https?:\/\//i.test(trimmedPath)) {
    return trimmedPath
  }

  const normalizedPath = trimmedPath.startsWith('/') ? trimmedPath : `/${trimmedPath}`
  const normalizedBaseURL = normalizeBaseURL(baseURL)
  if (normalizedBaseURL === '') {
    return normalizedPath
  }

  return `${normalizedBaseURL}${normalizedPath}`
}

function request(baseURL: string, input: string, init?: RequestInit): Promise<Response> {
  const trimmedInput = input.trim()
  const normalizedPath = trimmedInput.startsWith('/') ? trimmedInput : `/${trimmedInput}`

  const reqInit: RequestInit = {
    credentials: 'include',
    ...init,
  }

  return fetch(toAbsoluteURL(baseURL, input), reqInit).then((response) => {
    const isAuthRoute = [
      '/admin/auth/refresh',
      '/admin/auth/login',
      '/admin/auth/logout',
      '/admin/auth/session'
    ].some(route => normalizedPath.startsWith(route))

    // Cơ chế Tự Phục Hồi (Reactive Self-Healing) khi gặp lỗi 401
    if (response.status === 401 && !isAuthRoute) {
      const lockKey = 'admin_session_refresh_lock'
      const lockVal = localStorage.getItem(lockKey)
      const now = Date.now()
      let shouldRefresh = false

      if (!lockVal) {
        shouldRefresh = true
      } else {
        const lockTime = parseInt(lockVal)
        if (isNaN(lockTime) || now - lockTime > 10000) {
          shouldRefresh = true
        }
      }

      if (shouldRefresh) {
        localStorage.setItem(lockKey, now.toString())
        return fetch(toAbsoluteURL(baseURL, '/admin/auth/refresh'), {
          method: 'POST',
          credentials: 'include'
        }).then((refreshRes) => {
          localStorage.removeItem(lockKey)
          if (refreshRes.ok) {
            emitAdminSessionRefresh()
            // Gửi lại request ban đầu với cookie/session mới sinh
            return fetch(toAbsoluteURL(baseURL, input), reqInit)
          } else {
            emitAdminUnauthorized()
            return response
          }
        }).catch(() => {
          localStorage.removeItem(lockKey)
          emitAdminUnauthorized()
          return response
        })
      } else {
        // Tab khác đang thực hiện refresh, đợi 1.5 giây rồi thử gửi lại request ban đầu (retry)
        return new Promise<Response>((resolve) => {
          setTimeout(() => {
            resolve(fetch(toAbsoluteURL(baseURL, input), reqInit))
          }, 1500)
        }).then((retryRes) => {
          if (retryRes.status === 401) {
            emitAdminUnauthorized()
          }
          return retryRes
        })
      }
    }

    // Cơ chế Sliding Session chủ động dựa trên TTL còn lại (Proactive Silent Refresh)
    if (response.ok && response.headers.has('X-Session-Expires-In')) {
      const expiresIn = parseInt(response.headers.get('X-Session-Expires-In') ?? '')

      if (!isNaN(expiresIn) && expiresIn < 900 && !isAuthRoute) {
        const lockKey = 'admin_session_refresh_lock'
        const lockVal = localStorage.getItem(lockKey)
        const now = Date.now()
        let shouldRefresh = false

        if (!lockVal) {
          shouldRefresh = true
        } else {
          const lockTime = parseInt(lockVal)
          if (isNaN(lockTime) || now - lockTime > 10000) {
            shouldRefresh = true
          }
        }

        if (shouldRefresh) {
          localStorage.setItem(lockKey, now.toString())
          fetch(toAbsoluteURL(baseURL, '/admin/auth/refresh'), {
            method: 'POST',
            credentials: 'include'
          }).then((refreshRes) => {
            if (refreshRes.ok) {
              emitAdminSessionRefresh()
            }
          }).finally(() => {
            localStorage.removeItem(lockKey)
          })
        }
      }
    }

    return response
  })
}

export function Fetch(input: string, init?: RequestInit): Promise<Response> {
  return request(ControlplaneURL, input, init)
}

