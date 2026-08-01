import { Fetch } from '@/lib/fetch'
import { generateNonce, getOrCreateDeviceKeys, sha256Hex, signPayload } from '@/lib/crypto'

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

export async function retireCategory(categoryID: string, expectedVersion: number, otpCode: string): Promise<Pick<ServiceCategory, 'id' | 'state' | 'row_version'>> {
  const path = `/admin/critical/managed-services/catalog/categories/${encodeURIComponent(categoryID)}/retire`
  const bodyString = JSON.stringify({ expected_version: expectedVersion })
  const bodyHash = await sha256Hex(bodyString)
  const timestamp = Math.floor(Date.now() / 1000).toString()
  const nonce = generateNonce()
  const keys = await getOrCreateDeviceKeys()
  const signature = await signPayload(`POST\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`, keys.privateKey)
  const response = await Fetch(path, {
    method: 'POST', headers: {
      'Content-Type': 'application/json', 'X-Admin-Signature': signature,
      'X-Admin-Timestamp': timestamp, 'X-Admin-Nonce': nonce, 'X-Admin-StepUp-Code': otpCode,
    }, body: bodyString,
  })
  const body = await response.json().catch(() => ({})) as ResponseBody<Pick<ServiceCategory, 'id' | 'state' | 'row_version'>>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot retire category')
  return body.data
}
