"use client";

import { useEffect, useRef } from "react";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useRealtime } from "@/realtime/provider";
import type { JobNotificationPayload } from "@/realtime/contracts";

/**
 * Notifications are only cache invalidation hints. The Controlplane API remains
 * authoritative, so reconnects and out-of-order publications always converge
 * through a scoped refetch.
 */
export function useManagedServiceRealtime(queryKeys: readonly QueryKey[]): void {
  const queryClient = useQueryClient();
  const { subscribeToStream } = useRealtime();
  const acceptedVersionRef = useRef(new Map<string, number>());

  useEffect(() => {
    acceptedVersionRef.current.clear();
    return subscribeToStream("notification", "job.notification", (payload: JobNotificationPayload) => {
      if (payload.operation !== "managed_service.instance.execute") return;
      const resourceID = typeof payload.resource_id === "string" ? payload.resource_id : "";
      const operationID = typeof payload.transaction_id === "string" ? payload.transaction_id : "";
      const notificationID = typeof payload.notification_id === "string" ? payload.notification_id : "";
      const statusVersion = typeof payload.status_version === "number" && Number.isSafeInteger(payload.status_version)
        ? payload.status_version
        : undefined;
      if (!resourceID && !operationID && !notificationID) return;

      // A terminal event can arrive after a stale intermediate publication;
      // never let an older status overwrite a newer durable projection.
      if (notificationID && statusVersion !== undefined) {
        const previous = acceptedVersionRef.current.get(notificationID);
        if (previous !== undefined && statusVersion <= previous) return;
        acceptedVersionRef.current.set(notificationID, statusVersion);
        if (acceptedVersionRef.current.size > 2048) {
          const oldest = acceptedVersionRef.current.keys().next().value;
          if (oldest) acceptedVersionRef.current.delete(oldest);
        }
      }

      for (const queryKey of queryKeys) {
        void queryClient.invalidateQueries({ queryKey });
      }
    });
  }, [queryClient, queryKeys, subscribeToStream]);
}
