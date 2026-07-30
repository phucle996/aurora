import { Fetch } from '@/lib/fetch'

export type CatalogAuditEvent = {
  id: string
  actor: string
  critical_proof_id?: string | null
  action: string
  record_kind: string
  record_id: string
  record_version: number
  outcome: string
  error_code?: string | null
  occurred_at: string
}

type ResponseBody<T> = { data?: T; error?: string; message?: string }

export async function listAuditEvents(): Promise<CatalogAuditEvent[]> {
  const response = await Fetch('/admin/managed-services/catalog/audit?limit=100')
  const body = await response.json().catch(() => ({})) as ResponseBody<{ items: CatalogAuditEvent[] }>
  if (!response.ok) throw new Error(body.message || body.error || 'Cannot load catalog audit')
  return body.data?.items ?? []
}
