type AdminAuthListener = () => void

const unauthorizedListeners = new Set<AdminAuthListener>()
const refreshListeners = new Set<AdminAuthListener>()

export function subscribeAdminUnauthorized(listener: AdminAuthListener): () => void {
  unauthorizedListeners.add(listener)
  return () => {
    unauthorizedListeners.delete(listener)
  }
}

export function emitAdminUnauthorized(): void {
  for (const listener of unauthorizedListeners) {
    listener()
  }
}

export function subscribeAdminSessionRefresh(listener: AdminAuthListener): () => void {
  refreshListeners.add(listener)
  return () => {
    refreshListeners.delete(listener)
  }
}

export function emitAdminSessionRefresh(): void {
  for (const listener of refreshListeners) {
    listener()
  }
}

