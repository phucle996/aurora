import { fetchJSON } from "@/shared/api/http";
import { criticalFetchJSON } from "@/shared/api/critical";

export type MailDesiredState = "paused" | "enabled";
export type MailSourceType = "kafka" | "redis_stream" | "nats_jetstream" | "rabbitmq";
export type MailRuntimeState = "stopped" | "starting" | "running" | "paused" | "draining" | "error" | "degraded";

export type MailConsumerRuntime = {
  runtime_epoch: string;
  runtime_revision: number;
  state: MailRuntimeState;
  active_instances: number;
  consumer_lag: number;
  error_code: string;
  error_message: string;
  observed_at: string;
  expires_at: string;
};

export type MailConsumerRuntimeWatch = {
  consumer_id: string;
  config_version: number;
  watch_lease_id: string;
  watch_ttl_seconds: number;
  runtime: MailConsumerRuntime | null;
};

export type MailConsumer = {
  id: string;
  workspace_id: string;
  code: string;
  name: string;
  source_type: MailSourceType;
  broker_resource_id: string;
  source_configured: boolean;
  topic: string;
  consumer_group: string;
  template_id: string;
  template_version: number;
  sender_profile_id: string;
  sender_version: number;
  desired_state: MailDesiredState;
  parallelism: number;
  config_version: number;
  config_sha256: string;
  created_at: string;
  updated_at: string;
  // [COMMENT]: Mutation responses carry the outbox event ID; read responses may omit it.
  operation_id?: string;
};

export type MailTemplate = {
  id: string;
  workspace_id: string | null;
  code: string;
  name: string;
  current_version: number;
  template_revision: number;
  created_at: string;
  updated_at: string;
};

export type MailTemplateVersion = {
  template_id: string;
  version: number;
  subject_template: string;
  html_template: string;
  content_sha256: string;
  created_at: string;
};

export type MailTemplateDetail = {
  template: MailTemplate;
  current_version: MailTemplateVersion;
  published_version?: MailTemplateVersion;
  operation_id?: string;
};

export type MailDeleteOperation = { consumer_id?: string; template_id?: string; operation_id: string };

export type ConsumerWrite = {
  name: string;
  source_type: MailSourceType;
  broker_resource_id: string;
  topic: string;
  consumer_group: string;
  template_id: string;
  template_version: number;
  sender_profile_id: string;
  sender_version: number;
  parallelism: number;
};

export type TemplateContentWrite = {
  subject_template: string;
  html_template: string;
};

type DataEnvelope<T> = { data?: T };
type CursorPage<T> = { items: T[]; next_cursor: string | number };

function requireData<T>(response: DataEnvelope<T>, message: string): T {
  if (response.data === undefined) throw new Error(message);
  return response.data;
}

export async function listMailConsumers(signal?: AbortSignal): Promise<MailConsumer[]> {
  const response = await fetchJSON<DataEnvelope<CursorPage<MailConsumer>>>(
    "/api/v1/mail/consumers?limit=200",
    { signal },
  );
  return requireData(response, "Mail consumers response is missing data").items;
}

export async function getMailConsumer(id: string, signal?: AbortSignal): Promise<MailConsumer> {
  const response = await fetchJSON<DataEnvelope<MailConsumer>>(
    `/api/v1/mail/consumers/${encodeURIComponent(id)}`,
    { signal },
  );
  return requireData(response, "Mail consumer detail is missing");
}

export async function watchMailConsumerRuntime(id: string, signal?: AbortSignal): Promise<MailConsumerRuntimeWatch> {
  const response = await fetchJSON<DataEnvelope<MailConsumerRuntimeWatch>>(
    `/api/v1/mail/consumers/${encodeURIComponent(id)}/runtime/watch`,
    { method: "POST", signal },
  );
  return requireData(response, "Mail consumer runtime watch response is missing");
}

export async function createMailConsumer(input: ConsumerWrite & { code: string }): Promise<MailConsumer & { operation_id: string }> {
  const response = await criticalFetchJSON<DataEnvelope<MailConsumer & { operation_id: string }>>(
    "/api/v1/critical/mail/consumers",
    { method: "POST", body: input },
  );
  return requireData(response, "Created mail consumer is missing");
}

export async function updateMailConsumer(id: string, input: ConsumerWrite & { desired_state: MailDesiredState; expected_config_version: number }): Promise<MailConsumer & { operation_id: string }> {
  const response = await criticalFetchJSON<DataEnvelope<MailConsumer & { operation_id: string }>>(
    `/api/v1/critical/mail/consumers/${encodeURIComponent(id)}`,
    { method: "PATCH", body: input },
  );
  return requireData(response, "Updated mail consumer is missing");
}

export async function changeMailConsumerState(id: string, action: "pause" | "resume", expectedConfigVersion: number): Promise<MailConsumer & { operation_id: string }> {
  const response = await criticalFetchJSON<DataEnvelope<MailConsumer & { operation_id: string }>>(
    `/api/v1/critical/mail/consumers/${encodeURIComponent(id)}/${action}`,
    { method: "POST", body: { expected_config_version: expectedConfigVersion } },
  );
  return requireData(response, "Updated mail consumer state is missing");
}

export async function deleteMailConsumer(id: string, expectedConfigVersion: number): Promise<MailDeleteOperation> {
  const response = await criticalFetchJSON<DataEnvelope<MailDeleteOperation>>(`/api/v1/critical/mail/consumers/${encodeURIComponent(id)}`, {
    method: "DELETE",
    body: { expected_config_version: expectedConfigVersion, drain_timeout_seconds: 30, reason: "console delete" },
  });
  return requireData(response, "Mail consumer delete operation is missing");
}

export async function listMailTemplates(signal?: AbortSignal): Promise<MailTemplate[]> {
  const response = await fetchJSON<DataEnvelope<CursorPage<MailTemplate>>>("/api/v1/mail/templates?limit=200", { signal });
  return requireData(response, "Mail templates response is missing data").items;
}

export async function getMailTemplate(id: string, signal?: AbortSignal): Promise<MailTemplateDetail> {
  const response = await fetchJSON<DataEnvelope<MailTemplateDetail>>(`/api/v1/mail/templates/${encodeURIComponent(id)}`, { signal });
  return requireData(response, "Mail template response is missing data");
}

export async function listMailTemplateVersions(id: string, signal?: AbortSignal): Promise<MailTemplateVersion[]> {
  const response = await fetchJSON<DataEnvelope<CursorPage<MailTemplateVersion>>>(
    `/api/v1/mail/templates/${encodeURIComponent(id)}/versions?limit=200`,
    { signal },
  );
  return requireData(response, "Mail template versions response is missing data").items;
}

export async function createMailTemplate(input: TemplateContentWrite & { code: string; name: string }): Promise<MailTemplateDetail & { operation_id: string }> {
  const response = await criticalFetchJSON<DataEnvelope<MailTemplateDetail & { operation_id: string }>>("/api/v1/critical/mail/templates", { method: "POST", body: input });
  return requireData(response, "Created mail template is missing");
}

export async function publishMailTemplate(id: string, expectedRevision: number, input: TemplateContentWrite): Promise<MailTemplateDetail & { operation_id: string }> {
  const response = await criticalFetchJSON<DataEnvelope<MailTemplateDetail & { operation_id: string }>>(
    `/api/v1/critical/mail/templates/${encodeURIComponent(id)}/versions`,
    { method: "POST", body: { ...input, expected_revision: expectedRevision } },
  );
  return requireData(response, "Published mail template is missing");
}

export async function deleteMailTemplate(id: string, expectedRevision: number): Promise<MailDeleteOperation> {
  const response = await criticalFetchJSON<DataEnvelope<MailDeleteOperation>>(`/api/v1/critical/mail/templates/${encodeURIComponent(id)}`, {
    method: "DELETE",
    body: { expected_revision: expectedRevision },
  });
  return requireData(response, "Mail template delete operation is missing");
}

export type MailConsumersSummary = {
  total: number;
  enabled: number;
  paused: number;
  needs_credentials: number;
  running: number;
  degraded: number;
  error: number;
  without_fresh_runtime: number;
  active_logical_slots: number;
};

export type MailTemplatesSummary = {
  total: number;
  in_use: number;
  unused: number;
  total_immutable_versions: number;
  recently_updated: number;
};

export async function getMailConsumersSummary(signal?: AbortSignal): Promise<MailConsumersSummary> {
  const response = await fetchJSON<DataEnvelope<MailConsumersSummary>>("/api/v1/mail/consumers/summary", { signal });
  return requireData(response, "Mail consumers summary is missing data");
}

export async function getMailTemplatesSummary(signal?: AbortSignal): Promise<MailTemplatesSummary> {
  const response = await fetchJSON<DataEnvelope<MailTemplatesSummary>>("/api/v1/mail/templates/summary", { signal });
  return requireData(response, "Mail templates summary is missing data");
}
