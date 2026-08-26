"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { createStorageAccessSession, getStorageAccessSessionReadiness } from "@/features/storage/api";
import { useUserSession } from "@/session/use-session";
import { useWorkspace } from "@/context/WorkspaceContext";
import { StorageGatewayError } from "@/features/storage/objects/api";
import { useRealtime } from "@/realtime/provider";

export type AccessSessionStatus = "idle" | "preparing" | "ready" | "expired" | "revoked" | "forbidden" | "gateway_degraded";

export function useStorageAccessSession(bucketId: string) {
  const { generation } = useUserSession();
  const { activeWorkspaceID } = useWorkspace();
  const { subscribeToStream } = useRealtime();
  const [status, setStatus] = useState<AccessSessionStatus>("idle");
  const [message, setMessage] = useState<string | null>(null);
  const [accessSessionID, setAccessSessionID] = useState<string | null>(null);
  const ownerKey = `${generation ?? "anonymous"}:${activeWorkspaceID ?? "none"}:${bucketId}`;
  const sessionRef = useRef<{ ownerKey: string; id: string; expiresAt: number } | null>(null);
  const refreshRef = useRef<{ ownerKey: string; promise: Promise<string> } | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const readinessWakeRef = useRef<{ accessSessionId: string; wake: () => void } | null>(null);

  useEffect(() => subscribeToStream("notification", "job.notification", (payload) => {
    if (payload.operation !== "storage.access.prepare" ||
      typeof payload.transaction_id !== "string" ||
      !["SUCCESS", "FAILED"].includes(typeof payload.status === "string" ? payload.status : "")) return;
    const waiter = readinessWakeRef.current;
    if (waiter?.accessSessionId === payload.transaction_id) waiter.wake();
  }), [subscribeToStream]);

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
    setAccessSessionID(null);
    const controller = new AbortController();
    abortRef.current = controller;
    signal?.addEventListener("abort", () => controller.abort(signal.reason), { once: true });
    setStatus("preparing");
    setMessage("Preparing the Zone access projection…");
    const refresh = createStorageAccessSession(bucketId, {
      durationSeconds: 900,
      actions: ["ListBucket", "GetObject", "PutObject", "DeleteObject", "GetObjectTagging", "PutObjectTagging"],
    }, controller.signal)
      .then(async (session) => {
        const expiresAt = Date.parse(session.expires_at);
        // An invalid/expired server timestamp must never become a reusable capability.
        if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) {
          throw new StorageGatewayError(502, "Storage access session has an invalid expiry.", "invalid");
        }
        const deadline = Math.min(expiresAt - 30_000, Date.now() + 35_000);
        let attempt = 0;
        while (Date.now() < deadline) {
          const readiness = await getStorageAccessSessionReadiness(bucketId, session.access_session_id, controller.signal);
          if (readiness.status === "ACTIVE") {
            sessionRef.current = { ownerKey, id: session.access_session_id, expiresAt };
            setAccessSessionID(session.access_session_id);
            setStatus("ready");
            setMessage(null);
            return session.access_session_id;
          }
          if (readiness.status === "FAILED") {
            throw new StorageGatewayError(503, `Storage access preparation failed${readiness.error_code ? ` (${readiness.error_code})` : ""}.`, "unavailable");
          }
          const waitMs = Math.min(2_000, 250 * 2 ** Math.min(attempt, 3));
          attempt += 1;
          await new Promise<void>((resolve, reject) => {
            let settled = false;
            const finish = () => {
              if (settled) return;
              settled = true;
              window.clearTimeout(timeout);
              controller.signal.removeEventListener("abort", abort);
              if (readinessWakeRef.current?.accessSessionId === session.access_session_id) readinessWakeRef.current = null;
              resolve();
            };
            const abort = () => {
              if (settled) return;
              settled = true;
              window.clearTimeout(timeout);
              if (readinessWakeRef.current?.accessSessionId === session.access_session_id) readinessWakeRef.current = null;
              reject(controller.signal.reason ?? new DOMException("The operation was aborted.", "AbortError"));
            };
            const timeout = window.setTimeout(finish, waitMs);
            readinessWakeRef.current = { accessSessionId: session.access_session_id, wake: finish };
            controller.signal.addEventListener("abort", abort, { once: true });
            if (controller.signal.aborted) abort();
          });
        }
        throw new StorageGatewayError(503, "Storage access is still being prepared. Try again shortly.", "preparing");
      })
      .catch((error: unknown) => {
		setStatus(error instanceof StorageGatewayError && error.status === 403
			? "forbidden"
			: error instanceof StorageGatewayError && error.code === "preparing"
				? "preparing"
				: "gateway_degraded");
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
    const accessSessionId = await ensure(signal);
    try {
      return await operation(accessSessionId, signal ?? new AbortController().signal);
    } catch (error) {
      lastError = error;
    }
    if (lastError instanceof StorageGatewayError && lastError.status === 403) {
      setStatus("forbidden");
      setMessage("Storage access is forbidden or billing admission is suspended.");
    }
    throw lastError;
  }, [ensure]);

  useEffect(() => {
    sessionRef.current = null;
    return () => {
      abortRef.current?.abort();
      readinessWakeRef.current = null;
      refreshRef.current = null;
      sessionRef.current = null;
    };
  }, [ownerKey]);

  return { status, message, accessSessionID, execute };
}
