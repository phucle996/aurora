import { Fetch } from '@/lib/fetch'

export type ServiceCategory = {
  id: string
  code: string
  name: Record<string, string>
  description: Record<string, string>
  icon_key: string
  state: string
  row_version: number
  created_at?: string
  updated_at?: string
}

type ResponseBody<T> = { data?: T; error?: string; message?: string }

export async function listCategories(): Promise<ServiceCategory[]> {
  const response = await Fetch('/admin/managed-services/catalog/categories?limit=100')
  const body = await response.json().catch(() => ({})) as ResponseBody<{ items: ServiceCategory[] }>
  if (!response.ok) throw new Error(body.message || body.error || 'Cannot load categories')
  return body.data?.items ?? []
}

export async function createCategory(input: {
  code: string
  name: Record<string, string>
  description: Record<string, string>
  icon_key: string
}): Promise<ServiceCategory> {
  const response = await Fetch('/admin/managed-services/catalog/categories', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  })
  const body = await response.json().catch(() => ({})) as ResponseBody<ServiceCategory>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot create category')
  return body.data
}
