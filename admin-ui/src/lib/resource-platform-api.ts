import { Fetch } from '@/lib/fetch'

export type RPDefinition = {
  id: string
  name: string
  slug: string
  description?: string
  category?: string
  status: string
  sort_order?: number
}

export type RPModel = {
  id: string
  definition_id: string
  name: string
  slug: string
  engine?: string
  status: string
}

export type RPVersion = {
  id: string
  model_id: string
  version: string
  is_default?: boolean
  status: string
}

export type RPTemplate = {
  id: string
  model_id: string
  version_id?: string
  name: string
  slug: string
  description?: string
  template_type?: string
  yaml_template?: string
  status: string
  is_default?: boolean
}

export type RPCluster = {
  id: string
  name: string
  slug: string
  region: string
  environment: string
  status: string
  endpoint?: string
}

export type RPJob = {
  id: string
  instance_id?: string
  workspace_id: string
  tenant_id: string
  job_type: string
  status: string
  retry_count: number
  max_retries: number
  error_message?: string
  created_at: string
  updated_at: string
}

export type RPJobLog = {
  id: string
  job_id: string
  level: string
  message: string
  metadata?: Record<string, unknown>
  created_at: string
}

type APIResponse<T> = {
  data?: T
  message?: string
  error?: string
}

async function readData<T>(resp: Response): Promise<T> {
  const body = await resp.json().catch(() => ({})) as APIResponse<T>
  if (!resp.ok) {
    throw new Error(body.message || body.error || 'Request failed')
  }
  return body.data as T
}

export const resourcePlatformAdminApi = {
  listDefinitions: () => Fetch('/admin/resource-platform/definitions').then((r) => readData<RPDefinition[]>(r)),
  listModels: (definitionID: string) => Fetch(`/admin/resource-platform/definitions/${definitionID}/models`).then((r) => readData<RPModel[]>(r)),
  listVersions: (modelID: string) => Fetch(`/admin/resource-platform/models/${modelID}/versions`).then((r) => readData<RPVersion[]>(r)),
  listTemplates: () => Fetch('/admin/resource-platform/templates').then((r) => readData<RPTemplate[]>(r)),
  listClusters: () => Fetch('/admin/resource-platform/clusters').then((r) => readData<RPCluster[]>(r)),
  listJobs: () => Fetch('/admin/resource-platform/jobs').then((r) => readData<RPJob[]>(r)),
  getJobLogs: (jobID: string) => Fetch(`/admin/resource-platform/jobs/${jobID}/logs`).then((r) => readData<RPJobLog[]>(r)),
  cancelJob: (jobID: string) => Fetch(`/admin/resource-platform/jobs/${jobID}/cancel`, { method: 'POST' }).then((r) => readData<unknown>(r)),
}
