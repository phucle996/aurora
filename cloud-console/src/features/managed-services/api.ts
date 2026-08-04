import { fetchJSON } from "@/shared/api/http";
import {
  decodeManagedServiceCatalogItem,
  decodeManagedServiceFormContract,
  decodeManagedServiceVersionContract,
  localizedText,
} from "@/features/managed-services/contract";
import type {
  ManagedServiceCatalogPage,
  FormDraftValue,
  ManagedServiceInstance,
  ManagedServiceInstanceSummary,
  ManagedServiceOperation,
  ManagedServiceOperationPage,
  ManagedServiceVersionContract,
} from "@/features/managed-services/model";

export async function listManagedServiceCatalog(cursor: string, signal?: AbortSignal): Promise<ManagedServiceCatalogPage> {
  const query = new URLSearchParams({ limit: "100" });
  if (cursor) query.set("cursor", cursor);
  const response = await fetchJSON<{ data?: { items?: unknown[]; next_cursor?: unknown } }>(`/api/v1/managed-services/catalog?${query.toString()}`, {
    method: "GET",
    signal,
    cache: "no-store",
  });
  if (!Array.isArray(response.data?.items) || typeof response.data.next_cursor !== "string" || response.data.next_cursor.length > 128) {
    throw new Error("Managed Service catalog response is invalid.");
  }
  return { items: response.data.items.map(decodeManagedServiceCatalogItem), next_cursor: response.data.next_cursor };
}

export async function getManagedServiceVersionContract(
  versionID: string,
  expectedRevisionID: string,
  signal?: AbortSignal,
): Promise<ManagedServiceVersionContract> {
  const response = await fetchJSON<{ data?: unknown }>(
    `/api/v1/managed-services/catalog/versions/${encodeURIComponent(versionID)}?expected_revision_id=${encodeURIComponent(expectedRevisionID)}`,
    { method: "GET", signal, cache: "no-store" },
  );
  return decodeManagedServiceVersionContract(response.data);
}

export { localizedText };

function managedRecord(value: unknown, message: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error(message);
  return value as Record<string, unknown>;
}

function managedString(value: unknown, message: string): string {
  if (typeof value !== "string" || value.length === 0 || value.length > 512) throw new Error(message);
  return value;
}

function managedNumber(value: unknown, message: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) throw new Error(message);
  return value;
}

function decodeManagedServiceOperation(value: unknown): ManagedServiceOperation {
  const source = managedRecord(value, "Managed Service operation response is invalid.");
  const operation: ManagedServiceOperation = {
    id: managedString(source.id, "Managed Service operation response is invalid."),
    kind: managedString(source.kind, "Managed Service operation response is invalid."),
    state: managedString(source.state, "Managed Service operation response is invalid."),
    generation: managedNumber(source.generation, "Managed Service operation response is invalid."),
    attempt: managedNumber(source.attempt, "Managed Service operation response is invalid."),
  };
  if (source.delivery_epoch !== undefined) operation.delivery_epoch = managedNumber(source.delivery_epoch, "Managed Service operation response is invalid.");
  if (source.instance_id !== undefined) operation.instance_id = managedString(source.instance_id, "Managed Service operation response is invalid.");
  if (source.target_revision_id !== undefined) operation.target_revision_id = managedString(source.target_revision_id, "Managed Service operation response is invalid.");
  if (source.blueprint_revision_id !== undefined) operation.blueprint_revision_id = managedString(source.blueprint_revision_id, "Managed Service operation response is invalid.");
  if (source.retry_of_operation_id !== undefined && source.retry_of_operation_id !== null) operation.retry_of_operation_id = managedString(source.retry_of_operation_id, "Managed Service operation response is invalid.");
  if (source.status_version !== undefined) operation.status_version = managedNumber(source.status_version, "Managed Service operation response is invalid.");
  if (source.last_error_code !== undefined && source.last_error_code !== null) operation.last_error_code = managedString(source.last_error_code, "Managed Service operation response is invalid.");
  if (source.last_sanitized_error !== undefined && source.last_sanitized_error !== null) operation.last_sanitized_error = managedString(source.last_sanitized_error, "Managed Service operation response is invalid.");
  if (source.completed_at !== undefined && source.completed_at !== null) operation.completed_at = managedString(source.completed_at, "Managed Service operation response is invalid.");
  if (source.created_at !== undefined) operation.created_at = managedString(source.created_at, "Managed Service operation response is invalid.");
  if (source.updated_at !== undefined) operation.updated_at = managedString(source.updated_at, "Managed Service operation response is invalid.");
  return operation;
}

function decodeAcceptedInstance(value: unknown) {
  const source = managedRecord(value, "Managed Service accepted response is invalid.");
  const desired = managedRecord(source.desired, "Managed Service accepted response is invalid.");
  const state = desired.state === undefined ? undefined : managedString(desired.state, "Managed Service accepted response is invalid.");
  const pendingRevisionID = desired.pending_revision_id === null || desired.pending_revision_id === undefined
    ? desired.pending_revision_id as string | null | undefined
    : managedString(desired.pending_revision_id, "Managed Service accepted response is invalid.");
  return {
    id: managedString(source.id, "Managed Service accepted response is invalid."),
    code: managedString(source.code, "Managed Service accepted response is invalid."),
    name: source.name === undefined ? undefined : managedString(source.name, "Managed Service accepted response is invalid."),
    desired: {
      state,
      generation: managedNumber(desired.generation, "Managed Service accepted response is invalid."),
      pending_revision_id: pendingRevisionID,
    },
  };
}

function decodeAcceptedOperation(value: unknown) {
  const source = managedRecord(value, "Managed Service accepted response is invalid.");
  return {
    id: managedString(source.id, "Managed Service accepted response is invalid."),
    kind: managedString(source.kind, "Managed Service accepted response is invalid."),
    state: managedString(source.state, "Managed Service accepted response is invalid."),
    generation: source.generation === undefined ? undefined : managedNumber(source.generation, "Managed Service accepted response is invalid."),
    attempt: source.attempt === undefined ? undefined : managedNumber(source.attempt, "Managed Service accepted response is invalid."),
    delivery_epoch: managedNumber(source.delivery_epoch, "Managed Service accepted response is invalid."),
  };
}

function decodeManagedServiceInstanceSummary(value: unknown): ManagedServiceInstanceSummary {
  const source = managedRecord(value, "Managed Service instance response is invalid.");
  const desired = managedRecord(source.desired, "Managed Service instance response is invalid.");
  const observed = managedRecord(source.observed, "Managed Service instance response is invalid.");
  const latest = source.latest_operation;
  return {
    id: managedString(source.id, "Managed Service instance response is invalid."),
    code: managedString(source.code, "Managed Service instance response is invalid."),
    name: managedString(source.name, "Managed Service instance response is invalid."),
    desired: {
      state: managedString(desired.state, "Managed Service instance response is invalid."),
      generation: managedNumber(desired.generation, "Managed Service instance response is invalid."),
      active_revision_id: desired.active_revision_id === null || desired.active_revision_id === undefined ? desired.active_revision_id as string | null | undefined : managedString(desired.active_revision_id, "Managed Service instance response is invalid."),
      pending_revision_id: desired.pending_revision_id === null || desired.pending_revision_id === undefined ? desired.pending_revision_id as string | null | undefined : managedString(desired.pending_revision_id, "Managed Service instance response is invalid."),
    },
    observed: {
      state: managedString(observed.state, "Managed Service instance response is invalid."),
      version: managedNumber(observed.version, "Managed Service instance response is invalid."),
      observed_at: observed.observed_at === null || observed.observed_at === undefined ? observed.observed_at as string | null | undefined : managedString(observed.observed_at, "Managed Service instance response is invalid."),
    },
    metadata_version: managedNumber(source.metadata_version, "Managed Service instance response is invalid."),
    latest_operation: latest === null || latest === undefined ? null : decodeManagedServiceOperation(latest),
    created_at: source.created_at === undefined ? undefined : managedString(source.created_at, "Managed Service instance response is invalid."),
    updated_at: source.updated_at === undefined ? undefined : managedString(source.updated_at, "Managed Service instance response is invalid."),
  };
}

function decodeManagedServiceInstance(value: unknown): ManagedServiceInstance {
  const source = managedRecord(value, "Managed Service instance response is invalid.");
  const summary = decodeManagedServiceInstanceSummary(source);
  const desired = managedRecord(source.desired, "Managed Service instance response is invalid.");
  const observed = managedRecord(source.observed, "Managed Service instance response is invalid.");
  const network = source.network_contract;
  const resize = source.resize_contract;
  return {
    ...summary,
    desired: { ...summary.desired, revision_sequence: managedNumber(desired.revision_sequence, "Managed Service instance response is invalid.") },
    observed: {
      ...summary.observed,
      output: observed.output === null || observed.output === undefined ? observed.output as Record<string, unknown> | null | undefined : managedRecord(observed.output, "Managed Service observed output is invalid."),
    },
    network_contract: network === null || network === undefined ? null : (() => {
      const sourceNetwork = managedRecord(network, "Managed Service network contract is invalid.");
      if (typeof sourceNetwork.namespace !== "string" || sourceNetwork.namespace.length > 253 || !Array.isArray(sourceNetwork.components)) throw new Error("Managed Service network contract is invalid.");
      return {
        namespace: sourceNetwork.namespace,
        components: sourceNetwork.components.map((component) => {
          const item = managedRecord(component, "Managed Service network contract is invalid.");
          if (typeof item.component_code !== "string" || typeof item.service_name !== "string" || !Array.isArray(item.ports)) throw new Error("Managed Service network contract is invalid.");
          return {
            component_code: item.component_code,
            service_name: item.service_name,
            pod_selector: item.pod_selector as Record<string, string> | string | null | undefined,
            ports: item.ports.map((port) => {
              const portRecord = managedRecord(port, "Managed Service network contract is invalid.");
              const portNumber = portRecord.port;
              if (typeof portRecord.name !== "string" || typeof portNumber !== "number" || !Number.isSafeInteger(portNumber) || portNumber < 1 || portNumber > 65535 || typeof portRecord.protocol !== "string") throw new Error("Managed Service network contract is invalid.");
              return { name: portRecord.name, port: portNumber, protocol: portRecord.protocol };
            }),
          };
        }),
      };
    })(),
    resize_contract: resize === null || resize === undefined ? null : decodeManagedServiceFormContract(resize),
  };
}

export async function listManagedServiceInstances(cursor = "", signal?: AbortSignal) {
  const query = new URLSearchParams({ limit: "100" });
  if (cursor) query.set("cursor", cursor);
  const response = await fetchJSON<{ data?: { items?: unknown[]; next_cursor?: unknown } }>(
    `/api/v1/managed-services/instances?${query.toString()}`,
    { method: "GET", signal, cache: "no-store" },
  );
  if (!Array.isArray(response.data?.items) || typeof response.data.next_cursor !== "string") throw new Error("Managed Service instance list response is invalid.");
  return { items: response.data.items.map(decodeManagedServiceInstanceSummary), next_cursor: response.data.next_cursor };
}

export async function getManagedServiceInstance(code: string, signal?: AbortSignal): Promise<ManagedServiceInstance> {
  const response = await fetchJSON<{ data?: { instance?: unknown } }>(
    `/api/v1/managed-services/instances/${encodeURIComponent(code)}`,
    { method: "GET", signal, cache: "no-store" },
  );
  return decodeManagedServiceInstance(response.data?.instance);
}

export async function listManagedServiceOperations(code: string, cursor = "", signal?: AbortSignal): Promise<ManagedServiceOperationPage> {
  const query = new URLSearchParams({ limit: "100" });
  if (cursor) query.set("cursor", cursor);
  const response = await fetchJSON<{ data?: { items?: unknown[]; next_cursor?: unknown } }>(
    `/api/v1/managed-services/instances/${encodeURIComponent(code)}/operations?${query.toString()}`,
    { method: "GET", signal, cache: "no-store" },
  );
  if (!Array.isArray(response.data?.items) || typeof response.data.next_cursor !== "string") throw new Error("Managed Service operation list response is invalid.");
  return { items: response.data.items.map(decodeManagedServiceOperation), next_cursor: response.data.next_cursor };
}

export async function createManagedServiceInstance(input: {
  code: string;
  name: string;
  blueprint_revision_id: string;
  input_schema_sha256: string;
  parameters: Record<string, FormDraftValue>;
}, signal?: AbortSignal) {
  const response = await fetchJSON<{ data?: { instance?: unknown; operation?: unknown } }>(
    `/api/v1/managed-services/instances`,
    { method: "POST", body: input, signal },
  );
  if (!response.data?.instance || !response.data.operation) throw new Error("Managed Service create response is invalid.");
  return { instance: decodeAcceptedInstance(response.data.instance), operation: decodeAcceptedOperation(response.data.operation) };
}

export async function renameManagedServiceInstance(code: string, input: { name: string; expected_metadata_version: number }, signal?: AbortSignal) {
  const response = await fetchJSON<{ data?: unknown }>(
    `/api/v1/managed-services/instances/${encodeURIComponent(code)}/name`,
    { method: "PATCH", body: input, signal },
  );
  const data = managedRecord(response.data, "Managed Service rename response is invalid.");
  return { id: managedString(data.id, "Managed Service rename response is invalid."), code: managedString(data.code, "Managed Service rename response is invalid."), name: managedString(data.name, "Managed Service rename response is invalid."), metadata_version: managedNumber(data.metadata_version, "Managed Service rename response is invalid.") };
}

export async function resizeManagedServiceInstance(code: string, input: { expected_generation: number; resources: Record<string, FormDraftValue> }, signal?: AbortSignal) {
  const response = await fetchJSON<{ data?: { instance?: unknown; operation?: unknown } }>(
    `/api/v1/managed-services/instances/${encodeURIComponent(code)}/resize`,
    { method: "POST", body: input, signal },
  );
  if (!response.data?.instance || !response.data.operation) throw new Error("Managed Service resize response is invalid.");
  return { instance: decodeAcceptedInstance(response.data.instance), operation: decodeAcceptedOperation(response.data.operation) };
}

export async function deleteManagedServiceInstance(code: string, expected_generation: number, signal?: AbortSignal) {
  const response = await fetchJSON<{ data?: { instance?: unknown; operation?: unknown } }>(
    `/api/v1/managed-services/instances/${encodeURIComponent(code)}`,
    { method: "DELETE", body: { expected_generation }, signal },
  );
  if (!response.data?.instance || !response.data.operation) throw new Error("Managed Service delete response is invalid.");
  return { instance: decodeAcceptedInstance(response.data.instance), operation: decodeAcceptedOperation(response.data.operation) };
}

export async function retryManagedServiceOperation(code: string, operationID: string, signal?: AbortSignal) {
  const response = await fetchJSON<{ data?: { operation?: unknown } }>(
    `/api/v1/managed-services/instances/${encodeURIComponent(code)}/operations/${encodeURIComponent(operationID)}/retry`,
    { method: "POST", signal },
  );
  if (!response.data?.operation) throw new Error("Managed Service retry response is invalid.");
  return decodeManagedServiceOperation(response.data.operation);
}
