"use client";

import { useMemo } from "react";
import { useWorkspace } from "@/context/WorkspaceContext";
import { useUserSession } from "@/session/use-session";

function zoneHint(): string {
  if (typeof document === "undefined") return "unknown";
  const entry = document.cookie.split("; ").find((value) => value.startsWith("zone_code="));
  if (!entry) return "unknown";
  try {
    const value = decodeURIComponent(entry.slice("zone_code=".length));
    return value.length <= 128 ? value : "invalid";
  } catch {
    return "invalid";
  }
}

export type ConsoleQueryScope = readonly ["console", string, string, string];

/**
 * Query keys carry the auth generation even when the endpoint itself derives
 * actor/context from cookies. This is a cache fence, not an authorization
 * claim; the backend remains the only authority.
 */
export function useConsoleQueryScope(): ConsoleQueryScope {
  const { generation } = useUserSession();
  const { activeWorkspaceID } = useWorkspace();
  const zone = zoneHint();
  return useMemo(
    () => ["console", generation ?? "anonymous", zone, activeWorkspaceID ?? "none"] as const,
    [activeWorkspaceID, generation, zone],
  );
}
