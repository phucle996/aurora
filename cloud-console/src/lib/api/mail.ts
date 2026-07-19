import { fetchJSON } from "./fetcher";

export type MailDesiredState = "paused" | "enabled" | "deleting";
export type MailTemplateStatus = "active" | "archived";

export type MailMessageMapping = {
  external_message_id_json_path: string;
  recipient_json_path: string;
  variable_json_paths: Record<string, string>;
};

export type MailConsumer = {
  id: string;
  workspace_id: string;
  name: string;
  source_type: "kafka";
  broker_resource_id: string;
  source_configured: boolean;
  topic: string;
  consumer_group: string;
  mapping: MailMessageMapping;
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
};

export type MailTemplate = {
  id: string;
  workspace_id: string | null;
  scope: "platform" | "workspace";
  name: string;
  current_version: number;
  template_revision: number;
  status: MailTemplateStatus;
  archived_at: string | null;
  created_at: string;
  updated_at: string;
};

export type MailTemplateVersion = {
  template_id: string;
  version: number;
  subject_template: string;
  text_template: string;
  html_template: string;
  variable_schema_json: unknown;
  content_sha256: string;
  created_at: string;
};

export type MailTemplateDetail = {
  template: MailTemplate;
  current_version: MailTemplateVersion;
};

export type ConsumerWrite = {
  name: string;
  source_type: "kafka";
  broker_resource_id: string;
  topic: string;
  consumer_group: string;
  mapping: MailMessageMapping;
  template_id: string;
  template_version: number;
  sender_profile_id: string;
  sender_version: number;
  parallelism: number;
};

export type TemplateContentWrite = {
  subject_template: string;
  text_template: string;
  html_template: string;
  variable_schema_json: unknown;
};

type DataEnvelope<T> = { data?: T };
type CursorPage<T> = { items: T[]; next_cursor: string | number };

// [COMMENT]: Go hiện encode json.RawMessage trong consumer response thành base64; adapter này
// cô lập transport quirk để component luôn nhận một mapping object ổn định.
function normalizeConsumer(consumer: Omit<MailConsumer, "mapping"> & { mapping: MailMessageMapping | string }): MailConsumer {
  if (typeof consumer.mapping !== "string") return consumer as MailConsumer;
  try {
    const binary = atob(consumer.mapping);
    const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
    return { ...consumer, mapping: JSON.parse(new TextDecoder().decode(bytes)) as MailMessageMapping };
  } catch {
    return {
      ...consumer,
      mapping: { external_message_id_json_path: "", recipient_json_path: "", variable_json_paths: {} },
    };
  }
}

function requireData<T>(response: DataEnvelope<T>, message: string): T {
  if (response.data === undefined) throw new Error(message);
  return response.data;
}

export async function listMailConsumers(signal?: AbortSignal): Promise<MailConsumer[]> {
  const response = await fetchJSON<DataEnvelope<CursorPage<Omit<MailConsumer, "mapping"> & { mapping: MailMessageMapping | string }>>>(
    "/api/v1/mail/consumers?limit=200",
    { signal },
  );
  return requireData(response, "Mail consumers response is missing data").items.map(normalizeConsumer);
}

export async function createMailConsumer(input: ConsumerWrite & { idempotency_key: string }): Promise<MailConsumer> {
  const response = await fetchJSON<DataEnvelope<Omit<MailConsumer, "mapping"> & { mapping: MailMessageMapping | string }>>(
    "/api/v1/mail/consumers",
    { method: "POST", body: input },
  );
  return normalizeConsumer(requireData(response, "Created mail consumer is missing"));
}

export async function updateMailConsumer(id: string, input: ConsumerWrite & { desired_state: MailDesiredState; expected_config_version: number }): Promise<MailConsumer> {
  const response = await fetchJSON<DataEnvelope<Omit<MailConsumer, "mapping"> & { mapping: MailMessageMapping | string }>>(
    `/api/v1/mail/consumers/${encodeURIComponent(id)}`,
    { method: "PATCH", body: input },
  );
  return normalizeConsumer(requireData(response, "Updated mail consumer is missing"));
}

export async function changeMailConsumerState(id: string, action: "pause" | "resume", expectedConfigVersion: number): Promise<MailConsumer> {
  const response = await fetchJSON<DataEnvelope<Omit<MailConsumer, "mapping"> & { mapping: MailMessageMapping | string }>>(
    `/api/v1/mail/consumers/${encodeURIComponent(id)}/${action}`,
    { method: "POST", body: { expected_config_version: expectedConfigVersion } },
  );
  return normalizeConsumer(requireData(response, "Updated mail consumer state is missing"));
}

export async function deleteMailConsumer(id: string, expectedConfigVersion: number): Promise<void> {
  await fetchJSON(`/api/v1/mail/consumers/${encodeURIComponent(id)}`, {
    method: "DELETE",
    body: { expected_config_version: expectedConfigVersion, drain_timeout_seconds: 30, reason: "console delete" },
  });
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

export async function createMailTemplate(input: TemplateContentWrite & { idempotency_key: string; name: string }): Promise<MailTemplateDetail> {
  const response = await fetchJSON<DataEnvelope<MailTemplateDetail>>("/api/v1/mail/templates", { method: "POST", body: input });
  return requireData(response, "Created mail template is missing");
}

export async function publishMailTemplate(id: string, expectedRevision: number, input: TemplateContentWrite): Promise<MailTemplateDetail> {
  const response = await fetchJSON<DataEnvelope<MailTemplateDetail>>(
    `/api/v1/mail/templates/${encodeURIComponent(id)}/versions`,
    { method: "POST", body: { ...input, expected_revision: expectedRevision } },
  );
  return requireData(response, "Published mail template is missing");
}

export async function archiveMailTemplate(id: string, expectedRevision: number): Promise<void> {
  await fetchJSON(`/api/v1/mail/templates/${encodeURIComponent(id)}/archive`, {
    method: "POST",
    body: { expected_revision: expectedRevision },
  });
}
