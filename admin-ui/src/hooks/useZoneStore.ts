import { create } from 'zustand'
import { Fetch } from '@/lib/fetch'

/**
 * Đọc giá trị cookie theo tên.
 * Trả về null nếu cookie không tồn tại hoặc giá trị rỗng.
 * Cookie zone_code được server set (HttpOnly=false) để client có thể đọc.
 */
function getCookieValue(name: string): string | null {
  if (typeof document === 'undefined') return null
  const match = document.cookie
    .split('; ')
    .find((row) => row.startsWith(`${name}=`))
  const val = match?.split('=')[1]
  return val ? decodeURIComponent(val) : null
}

/**
 * Ghi cookie trên client-side (non-HttpOnly).
 * SameSite=Lax đủ bảo mật cho luồng admin nội bộ.
 * Path=/ để cookie hợp lệ trên toàn bộ app.
 */
function setCookieValue(name: string, value: string | null, days = 7): void {
  if (typeof document === 'undefined') return
  if (value === null || value === 'global') {
    // Xóa cookie bằng cách set expires về quá khứ
    document.cookie = `${name}=; expires=Thu, 01 Jan 1970 00:00:00 UTC; path=/; SameSite=Lax`
  } else {
    const expires = new Date(Date.now() + days * 864e5).toUTCString()
    document.cookie = `${name}=${encodeURIComponent(value)}; expires=${expires}; path=/; SameSite=Lax`
  }
}

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

/**
 * Hydrate activeZone từ cookie zone_code ngay khi module được load.
 * Cookie là SOT (Source of Truth): nếu server đã set zone_code trong cookie
 * (ví dụ: edge-viet-nam-1) thì store phải phản ánh đúng giá trị đó ngay sau reload.
 * Giá trị 'global' trong cookie được map về null (= Global / All Zones).
 */
const initialZoneFromCookie = (() => {
  const raw = getCookieValue('zone_code')
  if (!raw || raw === 'global') return null
  return raw
})()

export const useZoneStore = create<ZoneState>((set, get) => ({
  zones: [],
  // Ưu tiên đọc cookie zone_code khi khởi tạo; fallback về null (Global)
  activeZone: initialZoneFromCookie,
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
        const resp = await Fetch('/admin/hierarchy/zones/catalog')
        if (!resp.ok) throw new Error('Cannot load zones.')
        const body = await resp.json()
        const rawItems = Array.isArray(body) ? body : (body.data?.items || [])
        const items: Zone[] = rawItems.map((item: Record<string, unknown>) => ({
          id: (item.id as string) || (item.code as string),
          code: item.code as string,
          name: item.name as string
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
    // Ghi lại vào cookie để đảm bảo nhất quán sau khi reload trang.
    // Cookie là SOT: in-memory store luôn đồng bộ theo cookie.
    setCookieValue('zone_code', zoneCode)
    set({ activeZone: zoneCode })
  },
}))

