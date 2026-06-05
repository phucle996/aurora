import { create } from 'zustand'
import { Fetch } from '@/lib/fetch'

export type Zone = {
  id: string
  name: string
}

type ZoneState = {
  zones: Zone[]
  activeZone: string | null // null đại diện cho Global - All Zones
  loading: boolean
  error: string
  fetchZones: () => Promise<void>
  setActiveZone: (zoneID: string | null) => void
}

export const useZoneStore = create<ZoneState>((set, get) => ({
  zones: [],
  activeZone: null,
  loading: false,
  error: '',
  fetchZones: async () => {
    // Nếu danh sách vùng đã được nạp sẵn, bỏ qua nạp lại
    if (get().zones.length > 0) return

    set({ loading: true, error: '' })
    try {
      const resp = await Fetch('/admin/core/zones/catalog')
      if (!resp.ok) throw new Error('Cannot load zones.')
      const body = await resp.json()
      const items: Zone[] = body.data?.items || []
      
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
    }
  },
  setActiveZone: (zoneID: string | null) => {
    set({ activeZone: zoneID })
  },
}))
