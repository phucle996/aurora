"use client";

import { useEffect } from "react";

import { mintStorageBucketRuntimeRead } from "@/features/storage/api";
import { publicRuntimeConfig } from "@/runtime-config";

const SNAPSHOT_SECONDS = 60;
const BYTES_PER_MEBIBYTE = 1_048_576;

export function useBucketRuntimeUsage(
  bucketId: string,
  onUsage: (usedMegabytes: string) => void,
  enabled: boolean,
) {
  useEffect(() => {
    if (!bucketId || !enabled) return;
    const controller = new AbortController();

    void (async () => {
      let retryAttempt = 0;
      while (!controller.signal.aborted) {
        let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
        try {
          const ticket = await mintStorageBucketRuntimeRead(
            bucketId,
            SNAPSHOT_SECONDS,
            controller.signal,
          );
          const baseDomain = publicRuntimeConfig()?.zonePublicBaseDomain ?? "";
          const expectedPath = `/zone-public/v1/runtime/storage/bucket/${bucketId}/metrics?from_seconds=${SNAPSHOT_SECONDS}`;
          const ticketExpiresAt = Date.parse(ticket.expires_at);
          if (
            !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(ticket.zone_code) ||
            baseDomain.length > 253 ||
            !baseDomain.split(".").every((label) =>
              /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label),
            ) ||
            ticket.method !== "GET" ||
            ticket.path !== expectedPath ||
            !Number.isFinite(ticketExpiresAt) ||
            ticketExpiresAt <= Date.now()
          ) {
            throw new Error("Storage runtime assertion response is invalid");
          }

          const response = await fetch(
            `https://${ticket.zone_code}.${baseDomain}${ticket.path}`,
            {
              method: "GET",
              headers: {
                Accept: "text/event-stream",
                "X-Aurora-Runtime-Assertion": ticket.assertion,
                "X-Aurora-Runtime-Signature": ticket.signature,
                "X-Aurora-Runtime-Key-Id": ticket.key_id,
              },
              credentials: "omit",
              cache: "no-store",
              signal: controller.signal,
            },
          );
          if (!response.ok || !response.body) {
            throw new Error("Zone storage runtime stream is unavailable");
          }

          reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";
          let eventType = "message";
          let dataLines: string[] = [];
          while (!controller.signal.aborted) {
            const chunk = await reader.read();
            buffer += decoder.decode(chunk.value ?? new Uint8Array(), {
              stream: !chunk.done,
            });
            let boundary = buffer.indexOf("\n");
            while (boundary >= 0) {
              const line = buffer.slice(0, boundary).replace(/\r$/, "");
              buffer = buffer.slice(boundary + 1);
              if (line === "") {
                if (
                  (eventType === "runtime.snapshot" || eventType === "runtime.metric") &&
                  dataLines.length > 0
                ) {
                  const frame = JSON.parse(dataLines.join("\n")) as {
                    payload?: { data?: { data?: { result?: unknown } } };
                  };
                  const result = frame.payload?.data?.data?.result;
                  if (Array.isArray(result)) {
                    let newestSampleAt = 0;
                    let newestBytes: number | null = null;
                    for (const series of result) {
                      if (!series || typeof series !== "object") continue;
                      const values = (series as { values?: unknown }).values;
                      if (!Array.isArray(values) || values.length === 0) continue;
                      const latest = values[values.length - 1];
                      if (!Array.isArray(latest) || latest.length < 2) continue;
                      const sampleAt = Number(latest[0]);
                      const bytes = Number(latest[1]);
                      if (
                        Number.isFinite(sampleAt) &&
                        Number.isFinite(bytes) &&
                        bytes >= 0 &&
                        sampleAt >= newestSampleAt
                      ) {
                        newestSampleAt = sampleAt;
                        newestBytes = bytes;
                      }
                    }
                    const sampleAgeMilliseconds =
                      Date.now() - newestSampleAt * 1_000;
                    if (
                      newestBytes !== null &&
                      sampleAgeMilliseconds >= 0 &&
                      sampleAgeMilliseconds <= SNAPSHOT_SECONDS * 1_000
                    ) {
                      onUsage((newestBytes / BYTES_PER_MEBIBYTE).toFixed(6));
                      retryAttempt = 0;
                    }
                  }
                } else if (eventType === "stream.error") {
                  throw new Error("Zone storage runtime stream expired");
                }
                eventType = "message";
                dataLines = [];
              } else if (line.startsWith("event:")) {
                eventType = line.slice(6).trim();
              } else if (line.startsWith("data:")) {
                dataLines.push(line.slice(5).trimStart());
              }
              boundary = buffer.indexOf("\n");
            }
            if (chunk.done) break;
          }
        } catch {
          if (controller.signal.aborted) return;
        } finally {
          if (reader) {
            try {
              await reader.cancel();
            } catch {
              // The stream may already be closed or aborted.
            }
          }
        }
        const retryDelay = Math.min(30_000, 1_000 * 2 ** retryAttempt);
        retryAttempt = Math.min(retryAttempt + 1, 5);
        await new Promise<void>((resolve) => {
          const onAbort = () => {
            window.clearTimeout(retry);
            resolve();
          };
          const retry = window.setTimeout(
            () => {
              controller.signal.removeEventListener("abort", onAbort);
              resolve();
            },
            retryDelay + Math.floor(Math.random() * 500),
          );
          controller.signal.addEventListener("abort", onAbort, { once: true });
        });
      }
    })();

    return () => controller.abort();
  }, [bucketId, enabled, onUsage]);
}
