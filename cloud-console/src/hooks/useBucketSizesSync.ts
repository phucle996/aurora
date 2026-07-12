"use client";

import { useEffect } from "react";
import { useRealtime } from "@/context/RealtimeContext";

interface SyncSizesPayload {
  sizes: Record<string, number>; // Map: bucket_name -> used_bytes
}

export const useBucketSizesSync = (
  userId: string | undefined,
  onSync: (sizes: Record<string, number>) => void
) => {
  const { centrifuge, isConnected } = useRealtime();

  useEffect(() => {
    if (!centrifuge || !isConnected || !userId) return;

    const channelName = `personal:${userId}`;
    console.log(`📡 Subscribing to personal channel: ${channelName}`);
    
    const sub = centrifuge.newSubscription(channelName);

    sub.on("publication", (ctx) => {
      console.log("📥 Realtime publication received:", ctx);
      
      // Khớp loại sự kiện được Notification Service chuyển tiếp từ NATS
      if (ctx.data && ctx.data.event_type === "storage.bucket.sizes.sync") {
        const payload = ctx.data.payload as SyncSizesPayload;
        if (payload && payload.sizes) {
          console.log("⚡ Auto-syncing updated bucket sizes:", payload.sizes);
          onSync(payload.sizes);
        }
      }
    });

    sub.subscribe();

    return () => {
      console.log(`📡 Unsubscribing from personal channel: ${channelName}`);
      sub.unsubscribe();
    };
  }, [centrifuge, isConnected, userId, onSync]);
};
