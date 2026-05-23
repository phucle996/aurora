import { emitAdminUnauthorized } from '@/lib/admin-auth-events'

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
    if (
      response.status === 401 &&
      normalizedPath !== '/admin/auth/login'
    ) {
      emitAdminUnauthorized()
    }

    return response
  })
}

export function Fetch(input: string, init?: RequestInit): Promise<Response> {
  return request(ControlplaneURL, input, init)
}
