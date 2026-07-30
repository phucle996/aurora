import { Fetch } from '@/lib/fetch'

export type ServiceVersion = {
  id: string
  definition_id: string
  code: string
  display_version: string
  name: Record<string, string>
  description: Record<string, string>
  icon_key: string
  state: string
  row_version: number
}

type ResponseBody<T> = { data?: T; error?: string; message?: string }

export async function listVersions(definitionID?: string): Promise<ServiceVersion[]> {
  const query = definitionID ? `?definition_id=${encodeURIComponent(definitionID)}&limit=100` : '?limit=100'
  const response = await Fetch(`/admin/managed-services/catalog/versions${query}`)
  const body = await response.json().catch(() => ({})) as ResponseBody<{ items: ServiceVersion[] }>
  if (!response.ok) throw new Error(body.message || body.error || 'Cannot load versions')
  return body.data?.items ?? []
}

export async function createVersion(input: {
  definition_id: string
  code: string
  display_version: string
  name: Record<string, string>
  description: Record<string, string>
  icon_key: string
}): Promise<ServiceVersion> {
  const response = await Fetch('/admin/managed-services/catalog/versions', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  })
  const body = await response.json().catch(() => ({})) as ResponseBody<ServiceVersion>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot create version')
  return body.data
}
