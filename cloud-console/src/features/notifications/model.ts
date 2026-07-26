import type { TimelineNotification } from "@/features/notifications/api";

export type NotificationItem = {
  id: string;
  title: string;
  message: string;
  type: "success" | "error" | "info" | "warning" | "processing";
  time: string;
  read: boolean;
  createdAt?: string;
};

function notificationType(value: string): NotificationItem["type"] {
  if (value === "success" || value === "error" || value === "processing") return value;
  return "info";
}

function formatTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown" : date.toLocaleString();
}

export function notificationItem(item: TimelineNotification): NotificationItem {
  return {
    id: item.notification_id,
    title: item.title,
    message: item.message,
    type: notificationType(item.severity),
    time: formatTime(item.created_at),
    read: Boolean(item.read_at),
    createdAt: item.created_at,
  };
}

export { formatTime };
