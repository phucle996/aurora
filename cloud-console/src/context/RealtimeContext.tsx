"use client";

import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { Centrifuge } from "centrifuge";
import { useUserSession } from "@/hooks/useUserSession";

export type RealtimeStream = "job" | "runtime";
type RealtimePayload = Record<string, unknown>;
type RealtimeCallback = (payload: unknown) => void;

interface RealtimeContextType {
  centrifuge: Centrifuge | null;
  isConnected: boolean;
  subscribeToStream: <T = RealtimePayload>(
    stream: RealtimeStream,
    eventType: string,
    callback: (payload: T) => void,
  ) => () => void;
}

function noopSubscribe<T>(
  _stream: RealtimeStream,
  _eventType: string,
  _callback: (payload: T) => void,
): () => void {
  void _stream;
  void _eventType;
  void _callback;
  return () => {};
}

const RealtimeContext = createContext<RealtimeContextType>({
  centrifuge: null,
  isConnected: false,
  subscribeToStream: noopSubscribe,
});

export const useRealtime = () => useContext(RealtimeContext);

function streamForChannel(channel: string | undefined): RealtimeStream | null {
  if (channel?.startsWith("jobs:")) return "job";
  if (channel?.startsWith("runtime:")) return "runtime";
  return null;
}

export const RealtimeProvider: React.FC<{
  children: React.ReactNode;
  userId: string | undefined;
}> = ({ children, userId }) => {
  const [centrifuge, setCentrifuge] = useState<Centrifuge | null>(null);
  const [isConnected, setIsConnected] = useState(false);
  const listenersRef = useRef<Record<string, Set<RealtimeCallback>>>({});

  const subscribeToStream = useCallback(
    <T = RealtimePayload,>(
      stream: RealtimeStream,
      eventType: string,
      callback: (payload: T) => void,
    ) => {
      void userId;
      const key = `${stream}:${eventType}`;
      if (!listenersRef.current[key]) {
        listenersRef.current[key] = new Set();
      }
      const listener = callback as unknown as RealtimeCallback;
      listenersRef.current[key].add(listener);

      return () => {
        const listeners = listenersRef.current[key];
        if (!listeners) return;
        listeners.delete(listener);
        if (listeners.size === 0) delete listenersRef.current[key];
      };
    },
    // A listener is scoped to the authenticated principal. Recreating this
    // function on identity changes forces consumers to unregister old-user
    // callbacks before the next user's channel can deliver a publication.
    [userId],
  );

  useEffect(() => {
    if (typeof window === "undefined" || !userId) {
      return;
    }

    const wsUrl = process.env.NEXT_PUBLIC_CENTRIFUGO_WS_URL;
    if (!wsUrl) {
      console.warn("NEXT_PUBLIC_CENTRIFUGO_WS_URL is not set, skipping connection");
      return;
    }

    const client = new Centrifuge(wsUrl);
    client.on("connected", () => {
      setCentrifuge(client);
      setIsConnected(true);
    });
    client.on("disconnected", () => {
      setCentrifuge(null);
      setIsConnected(false);
    });

    client.on("publication", (ctx) => {
      const stream = streamForChannel(ctx.channel);
      if (!stream || !ctx.data || typeof ctx.data !== "object") return;
      const data = ctx.data as RealtimePayload;

      const eventType =
        typeof data.event_type === "string" ? data.event_type : undefined;
      if (!eventType) return;

      // Channel is the authoritative stream boundary; the payload marker is
      // diagnostic and cannot move a runtime event into the durable job queue.
      if (data.stream && data.stream !== stream) return;
      const eventData = data.data !== undefined ? data.data : data;

      const fire = (key: string) => {
        listenersRef.current[`${stream}:${key}`]?.forEach((callback) => {
          try {
            callback(eventData);
          } catch (error) {
            console.error(`Error executing realtime callback [${stream}:${key}]`, error);
          }
        });
      };

      fire(eventType);
      const dotIndex = eventType.indexOf(".");
      if (dotIndex !== -1) {
        fire(`${eventType.substring(0, dotIndex)}.*`);
      }
    });

    client.connect();

    return () => {
      client.disconnect();
      setCentrifuge(null);
      setIsConnected(false);
    };
  }, [userId]);

  return (
    <RealtimeContext.Provider value={{ centrifuge, isConnected, subscribeToStream }}>
      {children}
    </RealtimeContext.Provider>
  );
};

export const RealtimeProviderWrapper: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { profile } = useUserSession();
  return <RealtimeProvider userId={profile?.user_id}>{children}</RealtimeProvider>;
};
