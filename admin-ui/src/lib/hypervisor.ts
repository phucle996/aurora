/**
 * hypervisor.ts — Client-side API integration for the Hypervisor module.
 *
 * Chỉ định nghĩa các hàm API và Types sạch sẽ, cần thiết để tương tác với
 * các API endpoints của backend Go controlplane.
 */

import { Fetch } from '@/lib/fetch'

// Định nghĩa DTO của Hypervisor Node nhận từ backend Go controlplane
export type HypervisorNodeItem = {
  id: string
  node_code: string
  name: string
  status: string
  cpu_cores_total: number
  cpu_cores_used: number
  ram_mb_total: number
  ram_mb_used: number
  storage_gb_total: number
  storage_gb_used: number
  last_active_at: string
  created_at: string
  updated_at: string
}

// Định nghĩa thông tin Zone hiển thị
export type ZoneOption = {
  id: string
  code: string
  name: string
  label: string
}

type APIEnvelope<T> = {
  message?: string
  error?: string
  data?: T
}

type TopologyZoneItem = {
  id: string
  code?: string
  name?: string
}

function messageFromEnvelope(payload: APIEnvelope<unknown>, fallback: string) {
  return payload.message || payload.error || fallback
}

async function readEnvelope<T>(response: Response, fallback: string): Promise<T> {
  let payload: APIEnvelope<T>
  try {
    payload = (await response.json()) as APIEnvelope<T>
  } catch {
    throw new Error(fallback)
  }

  if (!response.ok || payload.data === undefined) {
    throw new Error(messageFromEnvelope(payload, fallback))
  }

  return payload.data
}

// fetchHypervisorNodes lấy danh sách các nodes trong zone chỉ định
export async function fetchHypervisorNodes(zoneId: string): Promise<HypervisorNodeItem[]> {
  if (!zoneId || zoneId === 'global') {
    return []
  }
  const response = await Fetch(`/admin/hypervisor/nodes?zone_id=${encodeURIComponent(zoneId)}`)
  const data = await readEnvelope<{ nodes: HypervisorNodeItem[] }>(response, 'Cannot load hypervisor nodes')
  return data.nodes ?? []
}

// fetchTopologyZones lấy danh sách toàn bộ zones từ core platform để làm dropdown filter
export async function fetchTopologyZones(): Promise<ZoneOption[]> {
  const response = await Fetch('/admin/core/zones')
  const data = await readEnvelope<{ items?: TopologyZoneItem[] }>(response, 'Cannot load zones')
  return (data.items ?? []).map((item) => {
    const code = item.code || item.id
    const name = item.name || code
    return {
      id: item.id,
      code,
      name,
      label: `${code} · ${name}`,
    }
  })
}
