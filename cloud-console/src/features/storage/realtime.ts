"use client";

import { useEffect, useRef } from "react";
import { useRealtime, type BucketSizesPayload } from "@/realtime/provider";

export const useBucketSizesSync = (
  onSync: (sizes: Record<string, string>) => void
) => {
  const { subscribeToStream } = useRealtime();
  const pendingRef = useRef<Record<string, string>>({});
  const timerRef = useRef<number | null>(null);

  useEffect(() => {
    // [COMMENT]: Runtime channel chỉ chứa soft-state snapshot; không trộn với durable job result.
    // Khi nhận được sự kiện, dispatch trực tiếp đến handler của trang mà không cần subscribe/unsubscribe lại WebSocket
    const unsubscribe = subscribeToStream("runtime", "storage.bucket.sizes.sync", (payload: BucketSizesPayload) => {
      pendingRef.current = { ...pendingRef.current, ...payload.sizes };
      if (timerRef.current !== null) return;
      // Coalesce bursty soft-state publications; only the latest size per bucket matters.
      timerRef.current = window.setTimeout(() => {
        timerRef.current = null;
        const sizes = pendingRef.current;
        pendingRef.current = {};
        onSync(sizes);
      }, 100);
    });

    // Cleanup: hủy đăng ký listener khỏi Event Registry khi hook unmount
    return () => {
      unsubscribe();
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
      timerRef.current = null;
      pendingRef.current = {};
    };
  }, [onSync, subscribeToStream]);
};
