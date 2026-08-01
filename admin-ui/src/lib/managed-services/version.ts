import { Fetch } from '@/lib/fetch'
import { generateNonce, getOrCreateDeviceKeys, sha256Hex, signPayload } from '@/lib/crypto'

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

export async function deprecateVersion(versionID: string, expectedVersion: number, otpCode: string): Promise<Pick<ServiceVersion, 'id' | 'state' | 'row_version'>> {
  const path = `/admin/critical/managed-services/catalog/versions/${encodeURIComponent(versionID)}/deprecate`
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
  const body = await response.json().catch(() => ({})) as ResponseBody<Pick<ServiceVersion, 'id' | 'state' | 'row_version'>>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot deprecate version')
  return body.data
}

export async function retireVersion(versionID: string, expectedVersion: number, otpCode: string): Promise<Pick<ServiceVersion, 'id' | 'state' | 'row_version'>> {
  const path = `/admin/critical/managed-services/catalog/versions/${encodeURIComponent(versionID)}/retire`
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
  const body = await response.json().catch(() => ({})) as ResponseBody<Pick<ServiceVersion, 'id' | 'state' | 'row_version'>>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot retire version')
  return body.data
}
