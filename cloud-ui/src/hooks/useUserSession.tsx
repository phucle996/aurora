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

  useEffect(() => {
    mountedRef.current = true;
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
        if (mountedRef.current) setState(next);
      })
      .catch((error) => {
        window.clearTimeout(timeoutId);
        if (error instanceof DOMException && error.name === "AbortError") return;
        if (mountedRef.current) {
          const message = error instanceof Error ? error.message : "session check failed";
          setState({ status: "unauthenticated", reason: "error", message });
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
    if (mountedRef.current) setState(next);
    return next;
  }, []);

  const setAuthenticated = useCallback((session: UserSession) => {
    if (mountedRef.current) setState({ status: "authenticated", session });
  }, []);

  const clearSession = useCallback((reason?: "expired") => {
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
