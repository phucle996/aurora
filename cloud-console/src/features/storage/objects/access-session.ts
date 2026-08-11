"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { createStorageAccessSession } from "@/features/storage/api";
import { useUserSession } from "@/session/use-session";
import { useWorkspace } from "@/context/WorkspaceContext";
import { StorageGatewayError, sleep } from "@/features/storage/objects/api";

export type AccessSessionStatus = "idle" | "preparing" | "ready" | "expired" | "revoked" | "forbidden" | "gateway_degraded";

export function useStorageAccessSession(bucketId: string) {
  const { generation } = useUserSession();
  const { activeWorkspaceID } = useWorkspace();
  const [status, setStatus] = useState<AccessSessionStatus>("idle");
  const [message, setMessage] = useState<string | null>(null);
  const ownerKey = `${generation ?? "anonymous"}:${activeWorkspaceID ?? "none"}:${bucketId}`;
  const sessionRef = useRef<{ ownerKey: string; id: string; expiresAt: number } | null>(null);
  const refreshRef = useRef<{ ownerKey: string; promise: Promise<string> } | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const ensure = useCallback(async (signal?: AbortSignal): Promise<string> => {
    const cached = sessionRef.current;
    if (cached?.ownerKey === ownerKey && cached.expiresAt > Date.now() + 30_000) {
      setStatus("ready");
      return cached.id;
    }
    if (refreshRef.current?.ownerKey === ownerKey) return refreshRef.current.promise;
    if (refreshRef.current) {
      abortRef.current?.abort();
      refreshRef.current = null;
    }
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    signal?.addEventListener("abort", () => controller.abort(signal.reason), { once: true });
    setStatus("preparing");
    setMessage("Preparing the Zone access projection…");
    const refresh = createStorageAccessSession(bucketId, {
      durationSeconds: 900,
      actions: ["ListBucket", "GetObject", "PutObject", "DeleteObject", "GetObjectTagging", "PutObjectTagging"],
    }, controller.signal)
      .then((session) => {
        const expiresAt = Date.parse(session.expires_at);
        // An invalid/expired server timestamp must never become a reusable capability.
        if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) {
          throw new StorageGatewayError(502, "Storage access session has an invalid expiry.", "invalid");
        }
        sessionRef.current = { ownerKey, id: session.access_session_id, expiresAt };
        setStatus("ready");
        setMessage(null);
        return session.access_session_id;
      })
      .catch((error: unknown) => {
        setStatus(error instanceof StorageGatewayError && error.status === 403 ? "forbidden" : "gateway_degraded");
        setMessage(error instanceof Error ? error.message : "Unable to prepare storage access.");
        throw error;
      })
      .finally(() => {
        if (refreshRef.current?.promise === refresh) refreshRef.current = null;
        if (abortRef.current === controller) abortRef.current = null;
      });
    refreshRef.current = { ownerKey, promise: refresh };
    return refresh;
  }, [bucketId, ownerKey]);

  const execute = useCallback(async <T,>(operation: (accessSessionId: string, signal: AbortSignal) => Promise<T>, signal?: AbortSignal): Promise<T> => {
    let lastError: unknown;
    for (let attempt = 0; attempt < 5; attempt += 1) {
      const accessSessionId = await ensure(signal);
      try {
        return await operation(accessSessionId, signal ?? new AbortController().signal);
      } catch (error) {
        lastError = error;
        if (!(error instanceof StorageGatewayError) || error.status !== 403 || attempt === 4) break;
        setStatus("preparing");
        setMessage("Waiting for the Zone access projection…");
        await sleep(250 * 2 ** attempt, signal);
      }
    }
    if (lastError instanceof StorageGatewayError && lastError.status === 403) {
      setStatus("forbidden");
      setMessage("Storage access is forbidden or the Zone projection is not ready.");
    }
    throw lastError;
  }, [ensure]);

  useEffect(() => {
    sessionRef.current = null;
    return () => {
      abortRef.current?.abort();
      refreshRef.current = null;
      sessionRef.current = null;
    };
  }, [ownerKey]);

  return { status, message, execute };
}
