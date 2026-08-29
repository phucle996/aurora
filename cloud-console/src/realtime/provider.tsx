"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { Centrifuge } from "centrifuge";
import { useQueryClient } from "@tanstack/react-query";
import { useUserSession } from "@/session/use-session";
import { publicRuntimeConfig } from "@/runtime-config";
import { decodeEvent, dedupeKey, type EventCallback, type EventMap, type EventType, type RealtimeStatus, type RealtimeStream } from "@/realtime/contracts";
export type { JobNotificationPayload, MailRuntimePayload, RealtimeStatus, RealtimeStream } from "@/realtime/contracts";

type RealtimeContextValue = {
  status: RealtimeStatus;
  isConnected: boolean;
  subscribeToStream: <T extends EventType>(
    stream: RealtimeStream,
    eventType: T,
    callback: EventCallback<EventMap[T]>,
  ) => () => void;
};

const noopSubscribe = () => () => {};
const RealtimeContext = createContext<RealtimeContextValue>({
  status: "disabled",
  isConnected: false,
  subscribeToStream: noopSubscribe as RealtimeContextValue["subscribeToStream"],
});

export const useRealtime = () => useContext(RealtimeContext);

function streamForChannel(channel: string | undefined): RealtimeStream | null {
  if (channel?.startsWith("notifications:")) return "notification";
  if (channel?.startsWith("runtime:")) return "runtime";
  return null;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function RealtimeProvider({ children, userId, generation }: { children: React.ReactNode; userId?: string; generation?: string | null }) {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<RealtimeStatus>("disabled");
  const listenersRef = useRef(new Map<string, Set<(payload: unknown) => void>>());
  const seenRef = useRef(new Map<string, number>());

  const subscribeToStream = useCallback(<T extends EventType>(
    stream: RealtimeStream,
    eventType: T,
    callback: EventCallback<EventMap[T]>,
  ) => {
    const key = `${stream}:${eventType}`;
    const listeners = listenersRef.current.get(key) ?? new Set<(payload: unknown) => void>();
    const listener = callback as unknown as (payload: unknown) => void;
    const listenerCount = Array.from(listenersRef.current.values()).reduce((total, group) => total + group.size, 0);
    // A broken component cannot grow a process-lifetime listener queue without bound.
    if (!listeners.has(listener) && (listeners.size >= 64 || listenerCount >= 256)) return () => {};
    listeners.add(listener);
    listenersRef.current.set(key, listeners);
    return () => {
      const current = listenersRef.current.get(key);
      current?.delete(listener);
      if (current && current.size === 0) listenersRef.current.delete(key);
    };
  }, []);

  useEffect(() => {
    const listeners = listenersRef.current;
    const seen = seenRef.current;
    const scheduleStatus = (next: RealtimeStatus) => window.queueMicrotask(() => setStatus(next));
    if (typeof window === "undefined" || !userId || !generation) {
      window.queueMicrotask(() => setStatus("disabled"));
      listeners.clear();
      seen.clear();
      return;
    }

    const wsUrl = publicRuntimeConfig()?.centrifugoWsUrl.trim();
    if (!wsUrl) {
      scheduleStatus("degraded");
      return;
    }

    let client: Centrifuge;
    try {
      client = new Centrifuge(wsUrl);
    } catch {
      scheduleStatus("degraded");
      return;
    }

    scheduleStatus("connecting");
    client.on("connecting", () => setStatus("connecting"));
    client.on("connected", () => {
      setStatus("connected");
      // Publications are hints; a reconnect always reconciles durable query state.
      void queryClient.invalidateQueries({ type: "active" });
    });
    client.on("disconnected", (ctx) => {
      setStatus(ctx.code === 3501 || ctx.code === 3502 ? "unauthorized" : "reconnecting");
    });
    client.on("error", () => setStatus("degraded"));
    client.on("publication", (ctx) => {
      const stream = streamForChannel(ctx.channel);
      if (!stream || !isRecord(ctx.data)) return;
      const eventType = ctx.data.event_type;
      if (typeof eventType !== "string" || !["job.notification", "mail.consumer.runtime.changed"].includes(eventType)) return;
      if (ctx.data.stream !== undefined && ctx.data.stream !== stream) return;
      const payload = decodeEvent(eventType as EventType, ctx.data.data ?? ctx.data);
      if (!payload) return;

      const key = dedupeKey(eventType as EventType, payload);
      const now = Date.now();
      for (const [seenKey, expires] of seen) if (expires <= now) seen.delete(seenKey);
      if (key && seen.has(key)) return;
      if (key) {
        if (seen.size >= 2_048) {
          const oldest = seen.keys().next().value;
          if (oldest) seen.delete(oldest);
        }
        seen.set(key, now + 5 * 60 * 1000);
      }

      const fire = (name: string) => {
        for (const listener of listeners.get(`${stream}:${name}`) ?? []) {
          try {
            listener(payload);
          } catch {
            // One feature callback cannot block unrelated realtime consumers.
          }
        }
      };
      fire(eventType);
      const dot = eventType.indexOf(".");
      if (dot > 0) fire(`${eventType.slice(0, dot)}.*`);
    });

    client.connect();
    return () => {
      client.disconnect();
      listeners.clear();
      seen.clear();
      setStatus("disabled");
    };
  }, [generation, queryClient, userId]);

  const value = useMemo(
    () => ({ status, isConnected: status === "connected", subscribeToStream }),
    [status, subscribeToStream],
  );
  return <RealtimeContext.Provider value={value}>{children}</RealtimeContext.Provider>;
}

export function RealtimeProviderWrapper({ children }: { children: React.ReactNode }) {
  const { profile, generation } = useUserSession();
  return <RealtimeProvider userId={profile?.user_id} generation={generation}>{children}</RealtimeProvider>;
}
