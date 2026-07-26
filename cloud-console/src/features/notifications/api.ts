import { fetchJSON } from "@/shared/api/http";

export type TimelineNotification = {
  notification_id: string;
  activity_event_id: string;
  severity: string;
  title: string;
  message: string;
  operation: string;
  resource_id?: string | null;
  created_at: string;
  read_at?: string | null;
};

export type TimelineActivity = {
  event_id: string;
  category: string;
  action: string;
  actor_type: string;
  outcome: string;
  source_service: string;
  title: string;
  summary: string;
  occurred_at: string;
  metadata: Record<string, unknown>;
};

export type TimelinePage<T> = { items: T[]; next_cursor?: string | null };

export function listNotifications(cursor?: string, signal?: AbortSignal) {
  const query = new URLSearchParams({ limit: "50" });
  if (cursor) query.set("cursor", cursor);
  return fetchJSON<TimelinePage<TimelineNotification>>(`/api/v1/me/notifications?${query}`, { signal });
}

export function listUserActivities(cursor?: string, signal?: AbortSignal) {
  const query = new URLSearchParams({ limit: "50" });
  if (cursor) query.set("cursor", cursor);
  return fetchJSON<TimelinePage<TimelineActivity>>(`/api/v1/me/activities?${query}`, { signal });
}

export function markAllNotificationsRead(): Promise<void> {
  return fetchJSON<void>("/api/v1/me/notifications/read-all", { method: "PUT" });
}
