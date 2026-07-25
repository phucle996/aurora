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
  const { subscribeToStream, isConnected } = useRealtime();

  useEffect(() => {
    // [COMMENT]: Chỉ kích hoạt lắng nghe khi kết nối WebSocket đã sẵn sàng và có thông tin định danh người dùng
    if (!isConnected || !userId) return;

    console.log("📡 Registering local event listener for: storage.bucket.sizes.sync");

    // [COMMENT]: Runtime channel chỉ chứa soft-state snapshot; không trộn với durable job result.
    // Khi nhận được sự kiện, dispatch trực tiếp đến handler của trang mà không cần subscribe/unsubscribe lại WebSocket
    const unsubscribe = subscribeToStream("runtime", "storage.bucket.sizes.sync", (payload: SyncSizesPayload) => {
      if (payload && payload.sizes) {
        console.log("⚡ Auto-syncing updated bucket sizes globally dispatched:", payload.sizes);
        onSync(payload.sizes);
      }
    });

    // Cleanup: hủy đăng ký listener khỏi Event Registry khi hook unmount
    return () => {
      console.log("📡 Cleaning up local event listener for: storage.bucket.sizes.sync");
      unsubscribe();
    };
  }, [isConnected, userId, onSync, subscribeToStream]);
};
