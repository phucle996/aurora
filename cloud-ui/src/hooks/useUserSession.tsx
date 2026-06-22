"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import {
  UserUnauthorizedError,
  getUserSession,
  type UserSession,
} from "@/lib/api/session";

export type UserAuthState =
  | { status: "unknown" }
  | { status: "authenticated"; session: UserSession }
  | { status: "unauthenticated"; reason?: "expired" | "error"; message?: string };

type UserSessionContextValue = UserAuthState & {
  refreshSession: () => Promise<UserAuthState>;
  setAuthenticated: (session: UserSession) => void;
  clearSession: (reason?: "expired") => void;
};

const initialState: UserAuthState = { status: "unknown" };

const UserSessionContext = createContext<UserSessionContextValue | null>(null);

async function resolveSession(signal?: AbortSignal): Promise<UserAuthState> {
  try {
    const session = await getUserSession(signal);
    return { status: "authenticated", session };
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      throw error;
    }
    if (error instanceof UserUnauthorizedError) {
      return { status: "unauthenticated" };
    }
    const message = error instanceof Error ? error.message : "session check failed";
    return { status: "unauthenticated", reason: "error", message };
  }
}

export function UserSessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<UserAuthState>(initialState);
  const mountedRef = useRef(true);

  const clearSession = useCallback((reason?: "expired") => {
    // [COMMENT]: Reset trạng thái xác thực trong localStorage về false.
    if (typeof window !== "undefined") {
      window.localStorage.setItem("iam.user.authenticated", "false");
    }
    if (mountedRef.current) setState({ status: "unauthenticated", reason });
  }, []);

  // [COMMENT]: Đăng ký lắng nghe sự kiện iam:unauthorized toàn cục. Khi bất kỳ request nào bị
  // trả về lỗi 401 do phiên làm việc hết hạn, lập tức reset cache localStorage và đăng xuất.
  useEffect(() => {
    const handleUnauthorized = () => {
      clearSession();
    };
    if (typeof window !== "undefined") {
      window.addEventListener("iam:unauthorized", handleUnauthorized);
    }
    return () => {
      if (typeof window !== "undefined") {
        window.removeEventListener("iam:unauthorized", handleUnauthorized);
      }
    };
  }, [clearSession]);

  useEffect(() => {
    mountedRef.current = true;
    const controller = new AbortController();

    // [COMMENT]: Đọc nhanh trạng thái từ localStorage sau khi Client đã mount để tránh lỗi Hydration Mismatch.
    if (typeof window !== "undefined") {
      const cached = window.localStorage.getItem("iam.user.authenticated");
      const nextState: UserAuthState = cached === "true"
        ? { status: "authenticated", session: { authenticated: true } }
        : { status: "unauthenticated" };

      // [COMMENT]: Thiết lập nhanh trạng thái từ cache để tránh nhấp nháy UI (flickering) cho trang Dashboard.
      queueMicrotask(() => {
        if (mountedRef.current) {
          setState(nextState);
        }
      });

      // [COMMENT]: Next.js Server Middleware đã bị xóa để tối ưu hóa latency (gọi Envoy trực tiếp).
      // Do đó, ta chạy ngầm resolveSession() ngay sau khi mount để kiểm chứng trạng thái session thực tế với backend.
      if (cached === "true") {
        resolveSession(controller.signal)
          .then((actualState) => {
            if (mountedRef.current) {
              setState(actualState);
              if (actualState.status === "unauthenticated") {
                // [COMMENT]: Nếu session thực tế hết hạn, cập nhật lại localStorage để đồng bộ trạng thái đăng xuất.
                window.localStorage.setItem("iam.user.authenticated", "false");
              }
            }
          })
          .catch((err) => {
            // [COMMENT]: Bỏ qua lỗi abort khi unmount hoặc lỗi kết nối mạng tạm thời.
            if (err instanceof DOMException && err.name === "AbortError") {
              return;
            }
            console.warn("[Session Verification] Background check failed:", err);
          });
      }
    }

    return () => {
      mountedRef.current = false;
      controller.abort();
    };
  }, []);

  const refreshSession = useCallback(async () => {
    const controller = new AbortController();
    setState({ status: "unknown" });
    const next = await resolveSession(controller.signal);
    if (mountedRef.current) {
      setState(next);
      if (typeof window !== "undefined") {
        window.localStorage.setItem(
          "iam.user.authenticated",
          next.status === "authenticated" ? "true" : "false"
        );
      }
    }
    return next;
  }, []);

  const setAuthenticated = useCallback((session: UserSession) => {
    // [COMMENT]: Cập nhật trạng thái đã xác thực vào localStorage.
    if (typeof window !== "undefined") {
      window.localStorage.setItem("iam.user.authenticated", "true");
    }
    if (mountedRef.current) setState({ status: "authenticated", session });
  }, []);

  const value = useMemo<UserSessionContextValue>(
    () => ({
      ...state,
      refreshSession,
      setAuthenticated,
      clearSession,
    }),
    [state, refreshSession, setAuthenticated, clearSession],
  );

  return <UserSessionContext.Provider value={value}>{children}</UserSessionContext.Provider>;
}

export function useUserSession(): UserSessionContextValue {
  const ctx = useContext(UserSessionContext);
  if (!ctx) {
    throw new Error("useUserSession must be used within UserSessionProvider");
  }
  return ctx;
}
