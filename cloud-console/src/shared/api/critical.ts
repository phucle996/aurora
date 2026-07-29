import { signSessionProof } from "@/lib/security/deviceKey";
import { fetchJSON, type FetchJSONOptions } from "@/shared/api/http";

type SessionProofChallenge = {
  challenge_id: string;
  nonce: string;
  expires_in: number;
};

type CriticalOptions = Omit<FetchJSONOptions, "method" | "body" | "serializedBody"> & {
  method: "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
};

async function sha256Hex(value: string): Promise<string> {
  const digest = await window.crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export async function criticalFetchJSON<T>(path: string, options: CriticalOptions): Promise<T> {
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  if (!normalizedPath.startsWith("/api/v1/critical/") || normalizedPath.includes("?")) {
    throw new Error("Critical requests require a query-free /api/v1/critical/* path.");
  }

  const challenge = await fetchJSON<SessionProofChallenge>(
    "/api/v1/auth/session-proof/challenge",
    { method: "POST" },
  );
  if (
    !challenge ||
    typeof challenge.challenge_id !== "string" ||
    typeof challenge.nonce !== "string" ||
    typeof challenge.expires_in !== "number"
  ) {
    throw new Error("The session-proof challenge is invalid.");
  }

  // The hash and the outgoing request share this exact string; re-serializing would break the proof.
  const serializedBody = options.body === undefined ? "" : JSON.stringify(options.body);
  const timestamp = Math.floor(Date.now() / 1000);
  const message = [
    "aurora.session-proof.v1",
    challenge.challenge_id,
    challenge.nonce,
    options.method,
    normalizedPath,
    await sha256Hex(serializedBody),
    String(timestamp),
  ].join("\n");
  const headers = new Headers(options.headers);
  headers.set("x-session-proof-challenge-id", challenge.challenge_id);
  headers.set("x-session-proof-timestamp", String(timestamp));
  headers.set("x-session-proof-signature", await signSessionProof(message));

  return fetchJSON<T>(normalizedPath, {
    ...options,
    headers,
    serializedBody,
  });
}
