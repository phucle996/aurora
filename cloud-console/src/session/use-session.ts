"use client";

import { useUserSessionContext } from "@/session/context";

export type {
  SessionStatus,
  UserSessionContextValue,
  UserSessionState,
} from "@/session/context";

export function useUserSession() {
  return useUserSessionContext();
}
