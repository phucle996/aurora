"use client";

import { useEffect } from "react";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useRealtime } from "@/realtime/provider";
import type { NotificationItem } from "@/features/notifications/model";

export function useNotificationRealtime(queryKey: QueryKey): void {
  const queryClient = useQueryClient();
  const { subscribeToStream } = useRealtime();

  useEffect(() => subscribeToStream("notification", "job.notification", (payload) => {
    if (!payload.title && !payload.message) return;
    const id = payload.notification_id || payload.transaction_id;
    if (!id) return;
    const type: NotificationItem["type"] =
      payload.status === "SUCCESS" ? "success" :
        payload.status === "FAILED" ? "error" :
          payload.status === "PROCESSING" ? "processing" : "info";
    const next: NotificationItem = {
      id,
      title: payload.title || "System Event",
      message: payload.message || "",
      type,
      time: "Just now",
      read: false,
      createdAt: payload.created_at,
    };
    queryClient.setQueryData<NotificationItem[]>(queryKey, (current = []) => {
      const existing = current.findIndex((item) => item.id === id);
      if (existing < 0) return [next, ...current].slice(0, 50);
      return current.map((item, index) => index === existing ? { ...item, ...next, read: item.read } : item);
    });
  }), [queryClient, queryKey, subscribeToStream]);
}
