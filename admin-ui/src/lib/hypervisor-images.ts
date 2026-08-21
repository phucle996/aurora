import { Fetch } from '@/lib/fetch'

export type HypervisorImage = {
  id: string
  zone_id: string
  name: string
  code: string
  distribution: string
  release: string
  revision: number
  architecture: 'x86_64' | 'aarch64'
  format: 'qcow2' | 'raw'
  size_bytes: number
  sha256: string
  state: string
  provider_template_vmid?: number | null
  error_code?: string | null
  error_message?: string | null
  created_at: string
  updated_at: string
}

type Envelope<T> = { data?: T; message?: string; error?: string }

async function readEnvelope<T>(response: Response, fallback: string): Promise<T> {
  const payload = (await response.json()) as Envelope<T>
  if (!response.ok || payload.data === undefined) {
    throw new Error(payload.message || payload.error || fallback)
  }
  return payload.data
}

export async function listHypervisorImages(zoneID: string): Promise<HypervisorImage[]> {
  const response = await Fetch('/admin/hypervisor/images?limit=200', {
    headers: { 'x-zone-id': zoneID },
  })
  const data = await readEnvelope<{ images?: HypervisorImage[] }>(response, 'Cannot load image registry')
  return data.images ?? []
}

export type RegisterHypervisorImageInput = {
  name: string
  code: string
  distribution: string
  release: string
  revision: number
  architecture: 'x86_64' | 'aarch64'
  format: 'qcow2' | 'raw'
  size_bytes: number
  sha256: string
}

export async function registerHypervisorImage(
  zoneID: string,
  input: RegisterHypervisorImageInput,
): Promise<HypervisorImage> {
  const response = await Fetch('/admin/hypervisor/images', {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      'x-zone-id': zoneID,
    },
    body: JSON.stringify(input),
  })
  const data = await readEnvelope<HypervisorImage>(response, 'Cannot register image')
  return data
}

export async function importHypervisorImage(zoneID: string, imageID: string): Promise<void> {
  const response = await Fetch(
    `/admin/hypervisor/images/${encodeURIComponent(imageID)}/import`,
    {
      method: 'POST',
      headers: { 'x-zone-id': zoneID },
    },
  )
  await readEnvelope<unknown>(response, 'Cannot start image import')
}

export async function deleteHypervisorImage(zoneID: string, imageID: string): Promise<void> {
  const response = await Fetch(
    `/admin/hypervisor/images/${encodeURIComponent(imageID)}`,
    {
      method: 'DELETE',
      headers: { 'x-zone-id': zoneID },
    },
  )
  await readEnvelope<unknown>(response, 'Cannot delete image')
}

