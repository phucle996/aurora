type AdminAuthListener = () => void

const listeners = new Set<AdminAuthListener>()

export function subscribeAdminUnauthorized(listener: AdminAuthListener): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function emitAdminUnauthorized(): void {
  for (const listener of listeners) {
    listener()
  }
}
