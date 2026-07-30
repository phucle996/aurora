import { getOrCreateDeviceKeys, generateNonce, sha256Hex, signPayload } from '@/lib/crypto'
import { Fetch } from '@/lib/fetch'

export type BlueprintRevision = {
  id: string
  blueprint_id: string
  revision: number
  state: string
  row_version: number
  template_bundle_sha256: string
  contract_version: string
  contract_sha256: string
  validated_row_version?: number | null
  validated_at?: string | null
  published_at?: string | null
  retired_at?: string | null
  template_yaml?: string
  component_contract?: unknown[]
  input_schema?: Record<string, unknown>
  ui_schema?: Record<string, unknown>
  safe_observed_output_schema?: Record<string, unknown>
  zone_selector?: Record<string, unknown>
  capability_requirement?: Record<string, unknown>
}

export type DraftArtifact = {
  template_yaml: string
  contract_version: string
  component_contract: unknown[]
  input_schema: Record<string, unknown>
  ui_schema: Record<string, unknown>
  safe_observed_output_schema: Record<string, unknown>
  zone_selector: Record<string, unknown>
  capability_requirement: Record<string, unknown>
}

type ResponseBody<T> = { data?: T; error?: string; message?: string }

export async function listRevisions(blueprintID: string): Promise<BlueprintRevision[]> {
  const response = await Fetch(`/admin/managed-services/catalog/blueprints/${encodeURIComponent(blueprintID)}/revisions?limit=100`)
  const body = await response.json().catch(() => ({})) as ResponseBody<{ items: BlueprintRevision[] }>
  if (!response.ok) throw new Error(body.message || body.error || 'Cannot load revisions')
  return body.data?.items ?? []
}

export async function getDraft(draftID: string): Promise<BlueprintRevision> {
  const response = await Fetch(`/admin/managed-services/catalog/drafts/${encodeURIComponent(draftID)}`)
  const body = await response.json().catch(() => ({})) as ResponseBody<BlueprintRevision>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot load draft')
  return body.data
}

export async function createDraft(blueprintID: string, artifact: DraftArtifact, otpCode: string): Promise<BlueprintRevision> {
  const path = `/admin/critical/managed-services/catalog/blueprints/${encodeURIComponent(blueprintID)}/drafts`
  const bodyString = JSON.stringify(artifact)
  const bodyHash = await sha256Hex(bodyString)
  const timestamp = Math.floor(Date.now() / 1000).toString()
  const nonce = generateNonce()
  const keys = await getOrCreateDeviceKeys()
  const signature = await signPayload(`POST\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`, keys.privateKey)
  const response = await Fetch(path, {
    method: 'POST', headers: {
      'Content-Type': 'application/json', 'X-Admin-Signature': signature, 'X-Admin-Timestamp': timestamp,
      'X-Admin-Nonce': nonce, 'X-Admin-StepUp-Code': otpCode,
    }, body: bodyString,
  })
  const body = await response.json().catch(() => ({})) as ResponseBody<BlueprintRevision>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot create draft')
  return body.data
}

export async function patchDraft(draftID: string, expectedVersion: number, artifact: DraftArtifact, otpCode: string): Promise<BlueprintRevision> {
  const path = `/admin/critical/managed-services/catalog/drafts/${encodeURIComponent(draftID)}`
  const bodyString = JSON.stringify({ expected_version: expectedVersion, ...artifact })
  const bodyHash = await sha256Hex(bodyString)
  const timestamp = Math.floor(Date.now() / 1000).toString()
  const nonce = generateNonce()
  const keys = await getOrCreateDeviceKeys()
  const signature = await signPayload(`PATCH\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`, keys.privateKey)
  const response = await Fetch(path, {
    method: 'PATCH', headers: {
      'Content-Type': 'application/json', 'X-Admin-Signature': signature, 'X-Admin-Timestamp': timestamp,
      'X-Admin-Nonce': nonce, 'X-Admin-StepUp-Code': otpCode,
    }, body: bodyString,
  })
  const body = await response.json().catch(() => ({})) as ResponseBody<BlueprintRevision>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot update draft')
  return body.data
}

export async function validateDraft(draftID: string, expectedVersion: number, artifact: DraftArtifact, otpCode: string): Promise<BlueprintRevision> {
  const path = `/admin/critical/managed-services/catalog/drafts/${encodeURIComponent(draftID)}/validate`
  const bodyString = JSON.stringify({ expected_version: expectedVersion, ...artifact })
  const bodyHash = await sha256Hex(bodyString)
  const timestamp = Math.floor(Date.now() / 1000).toString()
  const nonce = generateNonce()
  const keys = await getOrCreateDeviceKeys()
  const signature = await signPayload(`POST\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`, keys.privateKey)
  const response = await Fetch(path, {
    method: 'POST', headers: {
      'Content-Type': 'application/json', 'X-Admin-Signature': signature, 'X-Admin-Timestamp': timestamp,
      'X-Admin-Nonce': nonce, 'X-Admin-StepUp-Code': otpCode,
    }, body: bodyString,
  })
  const body = await response.json().catch(() => ({})) as ResponseBody<BlueprintRevision>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot validate draft')
  return body.data
}

export async function publishDraft(draftID: string, expectedVersion: number, bundleHash: string, contractHash: string, otpCode: string): Promise<BlueprintRevision> {
  const path = `/admin/critical/managed-services/catalog/drafts/${encodeURIComponent(draftID)}/publish`
  const bodyString = JSON.stringify({ expected_version: expectedVersion, expected_bundle_sha256: bundleHash, expected_contract_sha256: contractHash })
  const bodyHash = await sha256Hex(bodyString)
  const timestamp = Math.floor(Date.now() / 1000).toString()
  const nonce = generateNonce()
  const keys = await getOrCreateDeviceKeys()
  const signature = await signPayload(`POST\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`, keys.privateKey)
  const response = await Fetch(path, {
    method: 'POST', headers: {
      'Content-Type': 'application/json', 'X-Admin-Signature': signature, 'X-Admin-Timestamp': timestamp,
      'X-Admin-Nonce': nonce, 'X-Admin-StepUp-Code': otpCode,
    }, body: bodyString,
  })
  const body = await response.json().catch(() => ({})) as ResponseBody<BlueprintRevision>
  if (!response.ok || !body.data) throw new Error(body.message || body.error || 'Cannot publish draft')
  return body.data
}
