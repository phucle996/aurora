import { create } from 'zustand'
import { Fetch } from '@/lib/fetch'

export type Zone = {
  id: string
  code: string
  name: string
}

type ZoneState = {
  zones: Zone[]
  activeZone: string | null // null = Global - All Zones; string = zone CODE (e.g. "vn-hanoi-1")
  loading: boolean
  error: string
  fetchZones: () => Promise<void>
  setActiveZone: (zoneCode: string | null) => void
}

// Promise cache để tránh duplicate concurrent API calls (Race Condition)
// Thường xảy ra trong React Strict Mode khi useEffect chạy 2 lần đồng thời
// hoặc khi nhiều components cùng gọi fetchZones lúc mount.
let activeFetchPromise: Promise<void> | null = null

export const useZoneStore = create<ZoneState>((set, get) => ({
  zones: [],
  activeZone: null,
  loading: false,
  error: '',
  fetchZones: async () => {
    // 1. Nếu danh sách vùng đã được nạp sẵn, không nạp lại
    if (get().zones.length > 0) return

    // 2. Nếu đang có một request fetchZones chạy song song, dùng chung Promise đó
    if (activeFetchPromise) {
      return activeFetchPromise
    }

    set({ loading: true, error: '' })

    activeFetchPromise = (async () => {
      try {
        const resp = await Fetch('/admin/core/zones/catalog')
        if (!resp.ok) throw new Error('Cannot load zones.')
        const body = await resp.json()
        const rawItems = Array.isArray(body) ? body : (body.data?.items || [])
        const items: Zone[] = rawItems.map((item: any) => ({
          id: item.id || item.code,
          code: item.code,
          name: item.name
        }))

        set({
          zones: items,
          loading: false
        })
      } catch (err) {
        console.error('Failed to load zones in global store:', err)
        set({
          error: err instanceof Error ? err.message : 'Failed to load zones',
          loading: false
        })
      } finally {
        // Giải phóng cache khi hoàn thành (thành công hoặc thất bại) để cho phép refetch sau này
        activeFetchPromise = null
      }
    })()

    return activeFetchPromise
  },
  // zoneCode: null = Global, string = e.g. "vn-hanoi-1"
  setActiveZone: (zoneCode: string | null) => {
    set({ activeZone: zoneCode })
  },
}))

