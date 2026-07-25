import { fetchJSON } from "./fetcher";

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

export type TimelinePage<T> = {
  items: T[];
  next_cursor?: string | null;
};

export async function listTimelineNotifications(
  cursor?: string,
  signal?: AbortSignal,
): Promise<TimelinePage<TimelineNotification>> {
  const query = new URLSearchParams({ limit: "50" });
  if (cursor) query.set("cursor", cursor);
  return fetchJSON<TimelinePage<TimelineNotification>>(
    `/api/v1/me/notifications?${query.toString()}`,
    { credentials: "include", signal },
  );
}

export async function listTimelineActivities(
  cursor?: string,
  signal?: AbortSignal,
): Promise<TimelinePage<TimelineActivity>> {
  const query = new URLSearchParams({ limit: "50" });
  if (cursor) query.set("cursor", cursor);
  return fetchJSON<TimelinePage<TimelineActivity>>(
    `/api/v1/me/activities?${query.toString()}`,
    { credentials: "include", signal },
  );
}

export async function markAllTimelineNotificationsRead(): Promise<void> {
  await fetchJSON<void>("/api/v1/me/notifications/read-all", {
    method: "PUT",
    credentials: "include",
  });
}

export async function markTimelineNotificationRead(
  notificationId: string,
  createdAt: string,
): Promise<void> {
  await fetchJSON<void>(
    `/api/v1/me/notifications/${encodeURIComponent(notificationId)}/read`,
    {
      method: "PUT",
      credentials: "include",
      body: { created_at: createdAt },
    },
  );
}
