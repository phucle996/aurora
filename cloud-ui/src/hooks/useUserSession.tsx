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
const sessionLoadingTimeoutMs = 8000;

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
  }, []);

  useEffect(() => {
    mountedRef.current = true;

    // [COMMENT]: Đọc nhanh trạng thái từ localStorage sau khi Client đã mount để tránh lỗi Hydration Mismatch.
    // Nếu có session cached, set ngay trạng thái để giao diện chuyển đổi mượt mà.
    if (typeof window !== "undefined") {
      const cached = window.localStorage.getItem("iam.user.authenticated");
      if (cached === "true") {
        setState({ status: "authenticated", session: { authenticated: true } });
      } else if (cached === "false") {
        setState({ status: "unauthenticated" });
      }
    }

    const controller = new AbortController();
    const timeoutId = window.setTimeout(() => {
      controller.abort();
      if (mountedRef.current) {
        setState({ status: "unauthenticated", reason: "error", message: "session check timeout" });
      }
    }, sessionLoadingTimeoutMs);

    void resolveSession(controller.signal)
      .then((next) => {
        window.clearTimeout(timeoutId);
        if (mountedRef.current) {
          setState(next);
          // [COMMENT]: Đồng bộ hoá lại localStorage dựa vào phản hồi thực tế từ Controlplane.
          if (typeof window !== "undefined") {
            window.localStorage.setItem(
              "iam.user.authenticated",
              next.status === "authenticated" ? "true" : "false"
            );
          }
        }
      })
      .catch((error) => {
        window.clearTimeout(timeoutId);
        if (error instanceof DOMException && error.name === "AbortError") return;
        if (mountedRef.current) {
          const message = error instanceof Error ? error.message : "session check failed";
          setState({ status: "unauthenticated", reason: "error", message });
          if (typeof window !== "undefined") {
            window.localStorage.setItem("iam.user.authenticated", "false");
          }
        }
      });

    return () => {
      mountedRef.current = false;
      window.clearTimeout(timeoutId);
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

  const clearSession = useCallback((reason?: "expired") => {
    // [COMMENT]: Reset trạng thái xác thực trong localStorage về false.
    if (typeof window !== "undefined") {
      window.localStorage.setItem("iam.user.authenticated", "false");
    }
    if (mountedRef.current) setState({ status: "unauthenticated", reason });
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
