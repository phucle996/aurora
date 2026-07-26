"use client";

import { createContext, useContext } from "react";
import type { RenderContext, UserProfile, UserSession } from "@/session/api";

export type SessionStatus = "verifying" | "authenticated" | "unauthenticated" | "error";

export type UserSessionState = {
  status: SessionStatus;
  loading: boolean;
  authenticated: boolean;
  generation: string | null;
  session: UserSession | null;
  renderContext: RenderContext | null;
  profile: UserProfile | null;
  error: string;
};

export type UserSessionContextValue = UserSessionState & {
  refreshSession: () => Promise<UserSessionState>;
  clearSession: (reason?: "logout" | "expired") => void;
  checkPermission: (matchKey: string, action: string) => boolean;
};

export const UserSessionContext = createContext<UserSessionContextValue | null>(null);

export function useUserSessionContext(): UserSessionContextValue {
  const context = useContext(UserSessionContext);
  if (!context) {
    throw new Error("useUserSession must be used within UserSessionProvider");
  }
  return context;
}
