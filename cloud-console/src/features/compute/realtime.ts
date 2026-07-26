"use client";

import { useEffect, useRef } from "react";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useRealtime } from "@/realtime/provider";

export function useComputeRealtime(queryKey: QueryKey): void {
  const queryClient = useQueryClient();
  const { subscribeToStream } = useRealtime();
  const timerRef = useRef<number | null>(null);

  useEffect(() => {
    const unsubscribe = subscribeToStream("notification", "job.notification", (payload) => {
      if (payload.operation !== "hypervisor.vm.create") return;
      if (timerRef.current !== null) return;
      timerRef.current = window.setTimeout(() => {
        timerRef.current = null;
        // Realtime is only a wake-up hint. PostgreSQL remains the durable VM state.
        void queryClient.invalidateQueries({ queryKey });
      }, 100);
    });
    return () => {
      unsubscribe();
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
      timerRef.current = null;
    };
  }, [queryClient, queryKey, subscribeToStream]);
}
