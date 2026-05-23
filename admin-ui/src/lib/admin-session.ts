import { Fetch } from '@/lib/fetch'

export type AdminSession = {
  authenticated: true
}

export class AdminUnauthorizedError extends Error {
  constructor() {
    super('Admin session is required.')
    this.name = 'AdminUnauthorizedError'
  }
}

export async function getAdminSession(signal?: AbortSignal): Promise<AdminSession> {
  const resp = await Fetch('/admin/auth/session', {
    method: 'GET',
    signal,
  })

  if (resp.status === 401) {
    throw new AdminUnauthorizedError()
  }
  if (!resp.ok) {
    throw new Error('Cannot verify admin session.')
  }

  let authenticated = false
  try {
    const payload = (await resp.json()) as { data?: { authenticated?: boolean } }
    authenticated = payload?.data?.authenticated === true
  } catch {
    authenticated = false
  }

  if (!authenticated) {
    throw new AdminUnauthorizedError()
  }

  return { authenticated: true }
}
