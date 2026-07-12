"use client";

import React, { createContext, useContext, useEffect, useState } from "react";
import { Centrifuge } from "centrifuge";
import { useUserSession } from "@/hooks/useUserSession";

interface RealtimeContextType {
  centrifuge: Centrifuge | null;
  isConnected: boolean;
}

const RealtimeContext = createContext<RealtimeContextType>({
  centrifuge: null,
  isConnected: false,
});

export const useRealtime = () => useContext(RealtimeContext);

export const RealtimeProvider: React.FC<{
  children: React.ReactNode;
  userId: string | undefined;
}> = ({ children, userId }) => {
  const [centrifuge, setCentrifuge] = useState<Centrifuge | null>(null);
  const [isConnected, setIsConnected] = useState(false);

  useEffect(() => {
    // Chỉ chạy ở môi trường trình duyệt (Client-side) và khi có userId của phiên đăng nhập
    if (typeof window === "undefined" || !userId) {
      setIsConnected(false);
      setCentrifuge(null);
      return;
    }

    const wsUrl = process.env.NEXT_PUBLIC_CENTRIFUGO_WS_URL;
    if (!wsUrl) {
      console.warn("🔌 NEXT_PUBLIC_CENTRIFUGO_WS_URL is not set, skipping connection");
      return;
    }

    console.log("🔌 Initializing Centrifugo connection to:", wsUrl);

    const client = new Centrifuge(wsUrl);

    client.on("connected", () => {
      console.log("🔌 Realtime connection established successfully");
      setIsConnected(true);
    });

    client.on("disconnected", () => {
      console.log("🔌 Realtime connection disconnected");
      setIsConnected(false);
    });

    client.connect();
    setCentrifuge(client);

    return () => {
      console.log("🔌 Cleaning up Centrifugo connection");
      client.disconnect();
    };
  }, [userId]);

  return (
    <RealtimeContext.Provider value={{ centrifuge, isConnected }}>
      {children}
    </RealtimeContext.Provider>
  );
};

// [COMMENT]: Wrapper giúp nạp userId từ Session Context và truyền vào RealtimeProvider ở phía client
export const RealtimeProviderWrapper: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { profile } = useUserSession();
  return <RealtimeProvider userId={profile?.user_id}>{children}</RealtimeProvider>;
};
