// =============================================================================
// centrifugo.ts — Client-side Centrifugo WebSocket Client
// =============================================================================

export type NotificationPayload = {
  status: string
  title: string
  message: string
  created_at: string
}

type Subscriber = (data: NotificationPayload) => void

class CentrifugoClient {
  private socket: WebSocket | null = null
  private subscribers = new Set<Subscriber>()
  private nextId = 1
  private reconnectTimeout: number | null = null
  private isConnecting = false

  constructor() {
    // Lazy initialization on first subscription
  }

  private getUrl(): string {
    const envUrl = import.meta.env.VITE_CENTRIFUGO_WS_URL
    if (envUrl) return envUrl
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const host = window.location.hostname || 'localhost'
    return `${protocol}//${host}:8000/connection/websocket`
  }

  public connect() {
    if (this.socket || this.isConnecting) return

    this.isConnecting = true
    const url = this.getUrl()
    console.log(`[Centrifugo] Connecting to ${url}...`)

    try {
      this.socket = new WebSocket(url)

      this.socket.onopen = () => {
        console.log('[Centrifugo] WebSocket connection opened. Sending connect handshake...')
        this.isConnecting = false
        // Send connect frame
        this.socket?.send(
          JSON.stringify({
            id: this.nextId++,
            connect: {},
          })
        )
      }

      this.socket.onmessage = (event) => {
        try {
          if (!event.data) return
          const payload = JSON.parse(event.data)
          // Handle push notification publication
          if (payload.push && payload.push.pub && payload.push.pub.data) {
            const data = payload.push.pub.data as NotificationPayload
            console.log('[Centrifugo] Received push notification:', data)
            this.subscribers.forEach((cb) => {
              try {
                cb(data)
              } catch (err) {
                console.error('[Centrifugo] Error in subscriber callback:', err)
              }
            })
          }
        } catch (err) {
          console.error('[Centrifugo] Error parsing message:', err)
        }
      }

      this.socket.onclose = (event) => {
        console.log(`[Centrifugo] Connection closed: code=${event.code}, reason=${event.reason}`)
        this.cleanup()
        // Only reconnect if we still have active subscribers
        if (this.subscribers.size > 0) {
          this.scheduleReconnect()
        }
      }

      this.socket.onerror = (err) => {
        console.error('[Centrifugo] WebSocket error:', err)
        this.socket?.close()
      }
    } catch (err) {
      console.error('[Centrifugo] Failed to establish WebSocket:', err)
      this.isConnecting = false
      if (this.subscribers.size > 0) {
        this.scheduleReconnect()
      }
    }
  }

  private cleanup() {
    if (this.socket) {
      this.socket.onopen = null
      this.socket.onmessage = null
      this.socket.onclose = null
      this.socket.onerror = null
      this.socket = null
    }
    this.isConnecting = false
  }

  private scheduleReconnect() {
    if (this.reconnectTimeout) return
    console.log('[Centrifugo] Scheduling reconnect in 3s...')
    this.reconnectTimeout = window.setTimeout(() => {
      this.reconnectTimeout = null
      this.connect()
    }, 3000)
  }

  /**
   * Subscribe to incoming notifications.
   * Connects automatically on first subscriber, and returns an unsubscribe function.
   */
  public subscribe(callback: Subscriber): () => void {
    this.subscribers.add(callback)
    this.connect()

    return () => {
      this.subscribers.delete(callback)
      if (this.subscribers.size === 0) {
        this.disconnect()
      }
    }
  }

  public disconnect() {
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout)
      this.reconnectTimeout = null
    }
    this.cleanup()
    console.log('[Centrifugo] Disconnected.')
  }
}

export const centrifugoClient = new CentrifugoClient()
