import { fetchJSON, type FetchJSONOptions } from "./fetcher";
import { signSessionProof } from "../security/deviceKey";

type CriticalChallenge = {
  challenge_id: string;
  nonce: string;
  expires_in: number;
};

type CriticalFetcherOptions = Omit<FetchJSONOptions, "serializedBody"> & {
  method: "POST" | "PUT" | "PATCH" | "DELETE";
};

async function sha256Hex(value: string): Promise<string> {
  const digest = await window.crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(value),
  );
  return Array.from(new Uint8Array(digest), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
}

// [COMMENT]: Base duy nhất cho mutation critical: lấy nonce mới, ký đúng body wire và gửi một lần.
export async function criticalFetcher<T>(
  path: string,
  options: CriticalFetcherOptions,
): Promise<T> {
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  if (!normalizedPath.startsWith("/api/v1/critical/")) {
    throw new Error("criticalFetcher only accepts /api/v1/critical/* routes");
  }
  if (normalizedPath.includes("?")) {
    // [COMMENT]: Query chưa nằm trong canonical v1 nên fail-closed, tránh ký path này nhưng gửi semantics khác.
    throw new Error("criticalFetcher v1 does not accept query parameters");
  }

  const challenge = await fetchJSON<CriticalChallenge>(
    "/api/v1/auth/session-proof/challenge",
    { method: "POST", credentials: "include", signal: options.signal },
  );
  const serializedBody =
    options.body === undefined ? "" : JSON.stringify(options.body);
  const timestamp = Math.floor(Date.now() / 1000);
  const bodyHash = await sha256Hex(serializedBody);
  const message = [
    "aurora.session-proof.v1",
    challenge.challenge_id,
    challenge.nonce,
    options.method.toUpperCase(),
    normalizedPath,
    bodyHash,
    String(timestamp),
  ].join("\n");
  const signature = await signSessionProof(message);
  const proofHeaders = new Headers(options.headers);
  proofHeaders.set("x-session-proof-challenge-id", challenge.challenge_id);
  proofHeaders.set("x-session-proof-timestamp", String(timestamp));
  proofHeaders.set("x-session-proof-signature", signature);

  return fetchJSON<T>(normalizedPath, {
    ...options,
    serializedBody,
    credentials: "include",
    headers: proofHeaders,
  });
}
