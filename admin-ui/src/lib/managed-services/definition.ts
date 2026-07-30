import { Fetch } from '@/lib/fetch'

export type ServiceDefinition = {
  id: string
  category_id: string
  code: string
  name: Record<string, string>
  description: Record<string, string>
  icon_key: string
  state: string
  row_version: number
}

type ResponseBody<T> = { data?: T; error?: string; message?: string }

export async function listDefinitions(categoryID?: string): Promise<ServiceDefinition[]> {
  const query = categoryID ? `?category_id=${encodeURIComponent(categoryID)}&limit=100` : '?limit=100'
  const response = await Fetch(`/admin/managed-services/catalog/definitions${query}`)
  const body = await response.json().catch(() => ({})) as ResponseBody<{ items: ServiceDefinition[] }>
  if (!response.ok) throw new Error(body.message || body.error || 'Cannot load definitions')
  return body.data?.items ?? []
}

export async function createDefinition(input: {
  category_id: string
  code: string
  name: Record<string, string>
  description: Record<string, string>
  icon_key: string
}): Promise<ServiceDefinition> {
  const response = await Fetch('/admin/managed-services/catalog/definitions', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  })
  const body = await response.json().catch(() => ({})) as ResponseBody<ServiceDefinition>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot create definition')
  return body.data
}
