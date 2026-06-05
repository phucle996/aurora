import { Fetch } from '@/lib/fetch'

const HypervisorBaseURL = (import.meta.env.VITE_HYPERVISOR_BACKEND_ORIGIN ?? '').trim()

function normalizeBaseURL(baseURL: string): string {
  if (baseURL === '') {
    return ''
  }
  return baseURL.replace(/\/+$/, '')
}

function toAbsoluteURL(baseURL: string, path: string): string {
  const normalizedPath = path.startsWith('/') ? path : `/${path}`
  const normalizedBaseURL = normalizeBaseURL(baseURL)
  if (normalizedBaseURL === '') {
    return normalizedPath
  }
  return `${normalizedBaseURL}${normalizedPath}`
}

export type HypervisorAgentItem = {
  id: string
  zone_id: string
  hostname: string
  status: string
  management_ip: string
  cpu_cores: number
  cpu_threads: number
  ram_gib: number
  ssd_gib: number
  running_vps: number
  agent_id: string
  agent_version: string
  agent_status: string
  last_heartbeat_at?: string
  vcpu_usage_percent: number
  memory_usage_percent: number
  storage_usage_percent: number
}

export type HypervisorAgentListResult = {
  items: HypervisorAgentItem[]
  page: number
  limit: number
  total: number
}

export type HypervisorOverviewSummary = {
  total_nodes: number
  healthy_nodes: number
  running_vps: number
  total_vcpu_capacity: number
  total_ram_gib: number
}

export type HypervisorZoneUtilization = {
  zone_id: string
  node_count: number
  vcpu_usage_percent: number
  memory_usage_percent: number
  storage_usage_percent: number
}

export type HypervisorAlert = {
  id: string
  agent_id: string
  hostname: string
  severity: string
  message: string
  status: string
  created_at: string
}

export type HypervisorOverview = {
  summary: HypervisorOverviewSummary
  zone_utilization: HypervisorZoneUtilization[]
  alerts: HypervisorAlert[]
}

export type BootstrapTokenResult = {
  bootstrap_token_id: string
  token: string
}

export type HypervisorEnrollmentNode = {
  id: string
  hostname: string
  status: string
  management_ip: string
  hardware: {
    cpu_model: string
    cpu_cores: number
    cpu_threads: number
    ram_model: string
    ram_gib: number
    disk_model: string
    ssd_gib: number
    gpu_model: string
    gpu_memory_gib: number
  }
  agent: {
    agent_id: string
    hostname: string
    version: string
    status: string
    last_heartbeat_at?: string
  }
}

export type HypervisorEnrollmentWaitResult = {
  matched: boolean
  ready: boolean
  phase: 'token_pending' | 'bootstrap_completed' | 'runtime_assignment_pending' | 'runtime_registered' | 'inventory_ready' | 'failed' | 'expired'
  reason?: string
  node?: HypervisorEnrollmentNode
}

export type HypervisorAgentMetric = {
  id: string
  agent_id: string
  cpu_used_percent: number
  cpu_used_cores: number
  ram_used_gib: number
  ram_used_percent: number
  ssd_used_gib: number
  ssd_used_percent: number
  gpu_used_gib: number
  gpu_used_percent: number
  network_rx_bps: number
  network_tx_bps: number
  disk_read_bps: number
  disk_write_bps: number
  load_avg_1m: number
  load_avg_5m: number
  load_avg_15m: number
  sampled_at: string
}

export type HypervisorStoragePool = {
  id: string
  name: string
  driver: string
  path: string
  total_gib: number
  status: string
  updated_at: string
}

export type HypervisorCPUPackage = {
  id: string
  package_index: number
  model: string
  cores: number
  threads: number
  created_at: string
  updated_at: string
}

export type HypervisorMemoryModule = {
  id: string
  slot_index: number
  model: string
  size_gib: number
  created_at: string
  updated_at: string
}

export type HypervisorGPUDevice = {
  id: string
  device_index: number
  model: string
  memory_gib: number
  core_count: number
  created_at: string
  updated_at: string
}

export type HypervisorNetworkInterface = {
  id: string
  name: string
  mac_address: string
  ipv4_address: string
  ipv6_address: string
  speed_mbps: number
  status: string
  updated_at: string
}

export type HypervisorVPSItem = {
  id: string
  name: string
  hostname: string
  status: string
  power_state: string
  vcpu_count: number
  ram_gib: number
  ssd_gib: number
  primary_ipv4: string
  primary_ipv6: string
  os_image: string
  created_at: string
}

export type HypervisorAgentDetailEvent = {
  id: string
  action: string
  target_type: string
  target_id: string
  message: string
  created_at: string
}

export type HypervisorAgentDetail = {
  agent: {
    id: string
    zone_id: string
    agent_id: string
    hostname: string
    management_ip: string
    listen_addr: string
    status: string
    version: string
    last_heartbeat_at?: string
    cert_not_after?: string
    created_at: string
    updated_at: string
  }
  latest_metric?: HypervisorAgentMetric
  cpu_packages: HypervisorCPUPackage[]
  memory_modules: HypervisorMemoryModule[]
  gpu_devices: HypervisorGPUDevice[]
  storage_pools: HypervisorStoragePool[]
  network_interfaces: HypervisorNetworkInterface[]
  vps_instances: HypervisorVPSItem[]
  recent_events: HypervisorAgentDetailEvent[]
}

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

export async function fetchHypervisorAgents(params: {
  page: number
  limit: number
  search: string
  zoneId: string
  status: string
}): Promise<HypervisorAgentListResult> {
  const searchParams = new URLSearchParams()
  searchParams.set('page', String(params.page))
  searchParams.set('limit', String(params.limit))
  if (params.search.trim()) {
    searchParams.set('search', params.search.trim())
  }
  if (params.zoneId.trim()) {
    searchParams.set('zone_id', params.zoneId.trim())
  }
  if (params.status.trim()) {
    searchParams.set('status', params.status.trim())
  }

  const response = await Fetch(`/admin/hypervisor/agents?${searchParams.toString()}`)
  return readEnvelope<HypervisorAgentListResult>(response, 'Cannot load hypervisor agents')
}

export async function fetchHypervisorOverview(): Promise<HypervisorOverview> {
  const response = await Fetch('/admin/hypervisor/overview')
  return readEnvelope<HypervisorOverview>(response, 'Cannot load hypervisor overview')
}

export async function createBootstrapToken(agentVersion: string | undefined, zoneId: string): Promise<BootstrapTokenResult> {
  const response = await Fetch('/admin/hypervisor/bootstrap-tokens', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ agent_version: agentVersion || '', zone_id: zoneId.trim() }),
  })
  return readEnvelope<BootstrapTokenResult>(response, 'Cannot create bootstrap token')
}

export async function waitHypervisorEnrollment(bootstrapTokenID: string, bootstrapToken: string): Promise<HypervisorEnrollmentWaitResult> {
  const response = await Fetch('/admin/hypervisor/enrollments/wait', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ bootstrap_token_id: bootstrapTokenID, bootstrap_token: bootstrapToken }),
  })
  return readEnvelope<HypervisorEnrollmentWaitResult>(response, 'Cannot wait for hypervisor enrollment')
}

export async function fetchHypervisorAgentDetail(agentId: string): Promise<HypervisorAgentDetail> {
  const response = await Fetch(`/admin/hypervisor/agents/${encodeURIComponent(agentId)}`)
  return readEnvelope<HypervisorAgentDetail>(response, 'Cannot load hypervisor agent')
}

export async function fetchHypervisorAgentMetrics(agentId: string, limit = 120): Promise<HypervisorAgentMetric[]> {
  const response = await Fetch(`/admin/hypervisor/agents/${encodeURIComponent(agentId)}/metrics?limit=${limit}`)
  const data = await readEnvelope<{ items: HypervisorAgentMetric[] }>(response, 'Cannot load node metrics')
  return data.items ?? []
}

export function hypervisorAgentStreamURL(agentId: string): string {
  const httpURL = toAbsoluteURL(HypervisorBaseURL, `/admin/hypervisor/agents/${encodeURIComponent(agentId)}/stream`)
  const absolute = new URL(httpURL, window.location.origin)
  absolute.protocol = absolute.protocol === 'https:' ? 'wss:' : 'ws:'
  return absolute.toString()
}

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
export async function deleteHypervisorAgent(agentId: string): Promise<void> {
  const response = await Fetch(`/admin/hypervisor/agents/${encodeURIComponent(agentId)}`, {
    method: 'DELETE',
  })
  await readEnvelope(response, 'Cannot delete node')
}
