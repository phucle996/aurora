export type RealtimeStream = "notification" | "runtime";
export type RealtimeStatus = "disabled" | "connecting" | "connected" | "reconnecting" | "unauthorized" | "degraded";

export type JobNotificationPayload = Record<string, unknown> & {
  operation?: string;
  title?: string;
  message?: string;
  status?: string;
  transaction_id?: string;
  notification_id?: string;
  operation_id?: string;
  event_id?: string;
  resource_id?: string;
  status_version?: number;
  created_at?: string;
};

export type MailRuntimePayload = Record<string, unknown> & {
  consumer_id: string;
  config_version: number;
  runtime_epoch: string;
  runtime_revision: number;
  state: string;
  active_instances: number;
  consumer_lag: number;
  error_code: string;
  error_message: string;
  observed_at: string;
  expires_at: string;
};

export type EventMap = {
  "job.notification": JobNotificationPayload;
  "mail.consumer.runtime.changed": MailRuntimePayload;
};

export type EventType = keyof EventMap;
export type EventCallback<T> = (payload: T) => void;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function decodeEvent<T extends EventType>(eventType: T, value: unknown): EventMap[T] | null {
  if (!isRecord(value)) return null;
  if (eventType === "mail.consumer.runtime.changed") {
    const required = [
      "consumer_id", "config_version", "runtime_epoch", "runtime_revision", "state",
      "active_instances", "consumer_lag", "error_code", "error_message", "observed_at", "expires_at",
    ] as const;
    if (!required.every((key) => key in value)) return null;
    if (typeof value.consumer_id !== "string" || typeof value.config_version !== "number" ||
      typeof value.runtime_epoch !== "string" || typeof value.runtime_revision !== "number" ||
      typeof value.state !== "string" || typeof value.active_instances !== "number" ||
      typeof value.consumer_lag !== "number" || typeof value.error_code !== "string" ||
      typeof value.error_message !== "string" || typeof value.observed_at !== "string" ||
      typeof value.expires_at !== "string") return null;
    return value as EventMap[T];
  }
  // Notification payloads remain opaque beyond the bounded non-secret envelope;
  // feature adapters own their operation/resource validation.
  if (JSON.stringify(value).length > 128 * 1024) return null;
  if (!(value.event_id || value.operation_id || value.notification_id || value.transaction_id || value.resource_id)) return null;
  return value as EventMap[T];
}

export function dedupeKey(eventType: EventType, payload: EventMap[EventType]): string | null {
  const value = payload as Record<string, unknown>;
  if (eventType === "job.notification" && typeof value.notification_id === "string" && value.notification_id) {
    const version = typeof value.status_version === "number" && Number.isSafeInteger(value.status_version)
      ? `:${value.status_version}`
      : "";
    return `${eventType}:${value.notification_id}${version}`;
  }
  const stable = value.event_id ?? value.operation_id ?? value.notification_id ?? value.transaction_id;
  if (typeof stable === "string" && stable) return `${eventType}:${stable}`;
  if (eventType === "mail.consumer.runtime.changed" && typeof value.consumer_id === "string" && typeof value.runtime_revision === "number") {
    return `${eventType}:${value.consumer_id}:${value.runtime_epoch}:${value.runtime_revision}`;
  }
  return null;
}
