"use client";

import React, { createContext, useContext, useEffect, useState, useRef, useCallback } from "react";
import { Centrifuge } from "centrifuge";
import { useUserSession } from "@/hooks/useUserSession";

// Định nghĩa kiểu dữ liệu callback nhận payload của event
type RealtimeCallback = (payload: any) => void;

interface RealtimeContextType {
  centrifuge: Centrifuge | null;
  isConnected: boolean;
  // [COMMENT]: Hàm global đăng ký lắng nghe sự kiện cụ thể qua eventType, trả về hàm hủy đăng ký (cleanup)
  subscribeToEvent: (eventType: string, callback: RealtimeCallback) => () => void;
}

const RealtimeContext = createContext<RealtimeContextType>({
  centrifuge: null,
  isConnected: false,
  subscribeToEvent: () => () => {},
});

export const useRealtime = () => useContext(RealtimeContext);

export const RealtimeProvider: React.FC<{
  children: React.ReactNode;
  userId: string | undefined;
}> = ({ children, userId }) => {
  const [centrifuge, setCentrifuge] = useState<Centrifuge | null>(null);
  const [isConnected, setIsConnected] = useState(false);

  // [COMMENT]: Lưu trữ danh sách listeners đăng ký theo từng eventType dưới dạng Set để đảm bảo không trùng lặp
  const listenersRef = useRef<Record<string, Set<RealtimeCallback>>>({});

  // [COMMENT]: Định nghĩa hàm đăng ký sự kiện, sử dụng useCallback để tránh tạo lại hàm khi re-render
  const subscribeToEvent = useCallback((eventType: string, callback: RealtimeCallback) => {
    if (!listenersRef.current[eventType]) {
      listenersRef.current[eventType] = new Set();
    }
    listenersRef.current[eventType].add(callback);

    // Trả về hàm dọn dẹp (cleanup) để xóa listener khi component unmount
    return () => {
      const eventListeners = listenersRef.current[eventType];
      if (eventListeners) {
        eventListeners.delete(callback);
        if (eventListeners.size === 0) {
          delete listenersRef.current[eventType];
        }
      }
    };
  }, []);

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

    // Khởi tạo Centrifuge client không có token để bắt buộc dùng connect proxy qua cookie auth
    const client = new Centrifuge(wsUrl);

    client.on("connected", () => {
      console.log("🔌 Realtime connection established successfully");
      setIsConnected(true);
    });

    client.on("disconnected", () => {
      console.log("🔌 Realtime connection disconnected");
      setIsConnected(false);
    });

    // [COMMENT]: Vì Connect Proxy ở Backend (connect.rs) tự động trả về kênh `personal:${userId}`
    // trong danh sách channels kết nối, Centrifugo sẽ tự động đăng ký (Server-side subscribe) kênh này.
    // Client-side không được gọi `newSubscription` nữa để tránh trùng lặp và báo lỗi 'already subscribed'.
    // Thay vào đó, lắng nghe sự kiện `publication` trực tiếp trên client instance.
    client.on("publication", (ctx) => {
      console.log("📥 Global Realtime publication received (Server-side sub):", ctx);
      
      // [COMMENT]: Định tuyến sự kiện dựa theo event_type. Dùng ctx.data.data làm payload chính (khớp với JSON của backend).
      if (ctx.data && ctx.data.event_type) {
        const eventType = ctx.data.event_type;
        const callbacks = listenersRef.current[eventType];
        if (callbacks) {
          const eventData = ctx.data.data !== undefined ? ctx.data.data : ctx.data.payload;
          callbacks.forEach((cb) => {
            try {
              cb(eventData);
            } catch (err) {
              console.error(`Error executing realtime callback for event ${eventType}:`, err);
            }
          });
        }
      }
    });

    client.connect();
    setCentrifuge(client);

    return () => {
      console.log("🔌 Cleaning up Centrifugo connection");
      client.disconnect();
    };
  }, [userId]);

  return (
    <RealtimeContext.Provider value={{ centrifuge, isConnected, subscribeToEvent }}>
      {children}
    </RealtimeContext.Provider>
  );
};

// [COMMENT]: Wrapper giúp nạp userId từ Session Context và truyền vào RealtimeProvider ở phía client
export const RealtimeProviderWrapper: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { profile } = useUserSession();
  return <RealtimeProvider userId={profile?.user_id}>{children}</RealtimeProvider>;
};
