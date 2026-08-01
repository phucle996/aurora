import { getOrCreateDeviceKeys, generateNonce, sha256Hex, signPayload } from '@/lib/crypto'
import { Fetch } from '@/lib/fetch'

export type ServiceBlueprint = {
  id: string
  version_id: string
  code: string
  name: Record<string, string>
  description: Record<string, string>
  icon_key: string
  state: string
  row_version: number
  published_revision_id?: string | null
}

type ResponseBody<T> = { data?: T; error?: string; message?: string }

export async function getBlueprintByVersion(versionID: string): Promise<ServiceBlueprint | null> {
  const response = await Fetch(`/admin/managed-services/catalog/versions/${encodeURIComponent(versionID)}/blueprint`)
  if (response.status === 404) return null
  const body = await response.json().catch(() => ({})) as ResponseBody<ServiceBlueprint>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot load blueprint')
  return body.data
}

export async function createBlueprint(versionID: string, input: {
  code: string
  name: Record<string, string>
  description: Record<string, string>
  icon_key: string
}, otpCode: string): Promise<ServiceBlueprint> {
  const path = `/admin/critical/managed-services/catalog/versions/${encodeURIComponent(versionID)}/blueprints`
  const bodyString = JSON.stringify(input)
  const bodyHash = await sha256Hex(bodyString)
  const timestamp = Math.floor(Date.now() / 1000).toString()
  const nonce = generateNonce()
  const keys = await getOrCreateDeviceKeys()
  const signature = await signPayload(`POST\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`, keys.privateKey)
  const response = await Fetch(path, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json', 'X-Admin-Signature': signature,
      'X-Admin-Timestamp': timestamp, 'X-Admin-Nonce': nonce, 'X-Admin-StepUp-Code': otpCode,
    },
    body: bodyString,
  })
  const body = await response.json().catch(() => ({})) as ResponseBody<ServiceBlueprint>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot create blueprint')
  return body.data
}

export async function deleteBlueprint(blueprintID: string, expectedVersion: number, otpCode: string): Promise<void> {
  const path = `/admin/critical/managed-services/catalog/blueprints/${encodeURIComponent(blueprintID)}`
  const bodyString = JSON.stringify({ expected_version: expectedVersion })
  const bodyHash = await sha256Hex(bodyString)
  const timestamp = Math.floor(Date.now() / 1000).toString()
  const nonce = generateNonce()
  const keys = await getOrCreateDeviceKeys()
  const signature = await signPayload(`DELETE\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`, keys.privateKey)
  const response = await Fetch(path, {
    method: 'DELETE', headers: {
      'Content-Type': 'application/json', 'X-Admin-Signature': signature,
      'X-Admin-Timestamp': timestamp, 'X-Admin-Nonce': nonce, 'X-Admin-StepUp-Code': otpCode,
    }, body: bodyString,
  })
  const body = await response.json().catch(() => ({})) as ResponseBody<unknown>
  if (!response.ok) throw new Error(body.message || body.error || 'Cannot delete blueprint')
}
