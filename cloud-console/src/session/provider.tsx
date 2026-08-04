"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { usePathname, useRouter } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import {
  getRenderContext,
  getUserProfile,
  getUserSession,
  UserUnauthorizedError,
} from "@/session/api";
import {
  UserSessionContext,
  type UserSessionContextValue,
  type UserSessionState,
} from "@/session/context";

const VERIFY_TIMEOUT_MS = 8_000;
const CONTEXT_CHANNEL = "aurora.console.principal-context.v1";

const verifyingState: UserSessionState = {
  status: "verifying",
  loading: true,
  authenticated: false,
  generation: null,
  session: null,
  renderContext: null,
  profile: null,
  error: "",
};

function unauthenticatedState(error = ""): UserSessionState {
  return {
    status: error ? "error" : "unauthenticated",
    loading: false,
    authenticated: false,
    generation: null,
    session: null,
    renderContext: null,
    profile: null,
    error,
  };
}

function isPublicRoute(pathname: string): boolean {
  return pathname === "/signin" || pathname.startsWith("/activate");
}

function createGeneration(): string {
  return globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export function UserSessionProvider({ children }: { children: ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const queryClient = useQueryClient();
  const [state, setState] = useState<UserSessionState>(verifyingState);
  const stateRef = useRef(state);
  const attemptRef = useRef(0);
  const verifyAbortRef = useRef<AbortController | null>(null);
  const teardownRef = useRef<Promise<void> | null>(null);
  const contextChannelRef = useRef<BroadcastChannel | null>(null);

  const teardown = useCallback(
    (reason: "logout" | "expired" = "logout") => {
      if (teardownRef.current) return;

      // Incrementing first fences every completion created by the old principal.
      attemptRef.current += 1;
      verifyAbortRef.current?.abort();
      const nextState = unauthenticatedState();
      stateRef.current = nextState;
      setState(nextState);

      teardownRef.current = (async () => {
        try {
          await queryClient.cancelQueries();
        } finally {
          queryClient.clear();
          teardownRef.current = null;
        }
      })();

      if (reason === "expired") {
        toast.info("Session expired. Please sign in again.", {
          id: "user-session-expired",
          duration: 3_200,
        });
      }
    },
    [queryClient],
  );

  const verify = useCallback(async (): Promise<UserSessionState> => {
    const attempt = attemptRef.current + 1;
    attemptRef.current = attempt;
    verifyAbortRef.current?.abort();
    const controller = new AbortController();
    verifyAbortRef.current = controller;
    const pendingState: UserSessionState = {
      ...stateRef.current,
      status: "verifying",
      loading: true,
      error: "",
    };
    stateRef.current = pendingState;
    setState(pendingState);

    const timeoutId = window.setTimeout(() => controller.abort("session verification timeout"), VERIFY_TIMEOUT_MS);
    try {
      const session = await getUserSession(controller.signal);
      const [renderContext, profile] = await Promise.all([
        getRenderContext(controller.signal),
        getUserProfile(controller.signal),
      ]);

      if (controller.signal.aborted || attempt !== attemptRef.current) {
        return stateRef.current;
      }

      const previous = stateRef.current;
      const samePrincipal = previous.profile?.user_id === profile.user_id;
      const previousContext = previous.renderContext?.kind === "tenant"
        ? `tenant:${previous.renderContext.tenant_id}`
        : previous.renderContext?.kind;
      const nextContext = renderContext.kind === "tenant"
        ? `tenant:${renderContext.tenant_id}`
        : renderContext.kind;
      const sameContext = previousContext === nextContext;
      if (previous.authenticated && (!samePrincipal || !sameContext)) {
        // Principal and owner context are both cache boundaries. Cancel first
        // so a late response cannot repopulate a cleared generation.
        await queryClient.cancelQueries();
        queryClient.clear();
        if (attempt !== attemptRef.current) return stateRef.current;
      }

      const nextState: UserSessionState = {
        status: "authenticated",
        loading: false,
        authenticated: true,
        generation: samePrincipal && sameContext && previous.generation ? previous.generation : createGeneration(),
        session,
        renderContext,
        profile,
        error: "",
      };
      stateRef.current = nextState;
      setState(nextState);
      if (previous.authenticated && (!samePrincipal || !sameContext)) {
        contextChannelRef.current?.postMessage({ type: "principal-context-changed" });
      }
      return nextState;
    } catch (error) {
      if (attempt !== attemptRef.current) return stateRef.current;

      if (error instanceof UserUnauthorizedError) {
        const nextState = unauthenticatedState();
        stateRef.current = nextState;
        setState(nextState);
        return nextState;
      }

      const timedOut = controller.signal.aborted;
      const nextState = unauthenticatedState(
        timedOut
          ? "Session verification timed out. Retry when the control plane is available."
          : error instanceof Error
            ? error.message
            : "Cannot verify user session.",
      );
      stateRef.current = nextState;
      setState(nextState);
      return nextState;
    } finally {
      window.clearTimeout(timeoutId);
      if (verifyAbortRef.current === controller) verifyAbortRef.current = null;
    }
  }, [queryClient]);

  useEffect(() => {
    const bootstrap = window.setTimeout(() => void verify(), 0);
    return () => {
      window.clearTimeout(bootstrap);
      attemptRef.current += 1;
      verifyAbortRef.current?.abort();
    };
  }, [verify]);

  useEffect(() => {
    if (!("BroadcastChannel" in window)) return;
    const channel = new BroadcastChannel(CONTEXT_CHANNEL);
    contextChannelRef.current = channel;
    channel.onmessage = (event: MessageEvent<unknown>) => {
      if (
        typeof event.data === "object" &&
        event.data !== null &&
        "type" in event.data &&
        event.data.type === "principal-context-changed"
      ) {
        void verify();
      }
    };
    return () => {
      if (contextChannelRef.current === channel) contextChannelRef.current = null;
      channel.close();
    };
  }, [verify]);

  useEffect(() => {
    const handleUnauthorized = () => teardown("expired");
    window.addEventListener("iam:unauthorized", handleUnauthorized);
    return () => window.removeEventListener("iam:unauthorized", handleUnauthorized);
  }, [teardown]);

  useEffect(() => {
    if (state.status === "unauthenticated" && !isPublicRoute(pathname)) {
      if (pathname === "/settings/tenant-invitations/join" || pathname === "/personal/settings/tenant-invitations/join") {
        router.replace(`/signin?return_to=${encodeURIComponent(pathname + window.location.search)}`);
      } else {
        router.replace("/signin");
      }
    }
  }, [pathname, router, state.status]);

  const checkPermission = useCallback(
    (matchKey: string, action: string): boolean => {
      if (state.status !== "authenticated") return false;
      const matchParts = matchKey.split(":");
      if (matchParts.length !== 2) return false;

      return state.renderContext?.navigation.some((navigation) => {
        const navigationParts = navigation.key.split(":");
        return (
          navigationParts.length === 2 &&
          matchParts.every((part, index) => part === "*" || part === navigationParts[index]) &&
          navigation.actions.includes(action)
        );
      }) ?? false;
    },
    [state.renderContext, state.status],
  );

  const value = useMemo<UserSessionContextValue>(
    () => ({
      ...state,
      refreshSession: verify,
      clearSession: teardown,
      checkPermission,
    }),
    [checkPermission, state, teardown, verify],
  );

  return <UserSessionContext.Provider value={value}>{children}</UserSessionContext.Provider>;
}
