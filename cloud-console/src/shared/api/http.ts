import { controlplaneBaseURL } from "@/shared/api/config";

export type FetchJSONOptions = {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  serializedBody?: string;
  headers?: HeadersInit;
  credentials?: RequestCredentials;
  signal?: AbortSignal;
  timeoutMs?: number;
  cache?: RequestCache;
};

export class APIError extends Error {
  readonly status: number;
  readonly retryable: boolean;
  readonly requestId?: string;

  constructor(status: number, message: string, requestId?: string) {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.retryable = status === 0 || status === 408 || status === 425 || status === 429 || status >= 500;
    this.requestId = requestId;
  }
}

export function isAPIError(error: unknown): error is APIError {
  return error instanceof APIError;
}

function normalizedPath(path: string): string {
  if (/^[a-z][a-z\d+.-]*:/i.test(path) || path.startsWith("//")) {
    throw new Error("HTTP client only accepts same-origin relative paths.");
  }
  return path.startsWith("/") ? path : `/${path}`;
}

function withTimeout(signal: AbortSignal | undefined, timeoutMs: number): { signal: AbortSignal; cancel: () => void } {
  const controller = new AbortController();
  const timeout = globalThis.setTimeout(() => controller.abort("request timeout"), timeoutMs);
  const abort = () => controller.abort(signal?.reason);
  if (signal?.aborted) controller.abort(signal.reason);
  else signal?.addEventListener("abort", abort, { once: true });
  return {
    signal: controller.signal,
    cancel: () => {
      globalThis.clearTimeout(timeout);
      signal?.removeEventListener("abort", abort);
    },
  };
}

async function responseMessage(response: Response): Promise<string> {
  try {
    const text = await response.text();
    const stripped = text.startsWith(")]}',\n")
      ? text.slice(6)
      : text.startsWith(")]}',")
        ? text.slice(5)
        : text;
    const parsed = JSON.parse(stripped) as { message?: unknown; error_message?: unknown };
    if (typeof parsed.error_message === "string" && parsed.error_message.trim()) return parsed.error_message;
    if (typeof parsed.message === "string" && parsed.message.trim()) return parsed.message;
  } catch {
    // Error bodies are untrusted and must never be logged or surfaced verbatim.
  }
  return response.statusText || "request failed";
}

export async function fetchJSON<T>(path: string, options: FetchJSONOptions = {}): Promise<T> {
  const method = options.method ?? "GET";
  const requestPath = normalizedPath(path);
  const headers = new Headers(options.headers);
  headers.set("Accept", "application/json");
  if (method !== "GET" || options.body !== undefined || options.serializedBody !== undefined) {
    headers.set("Content-Type", headers.get("Content-Type") ?? "application/json");
  }
  if (method !== "GET") headers.set("X-Aurora-Requested-With", "cloud-console");

  const timeoutMs = options.timeoutMs ?? (method === "GET" ? 15_000 : 20_000);
  const timeout = withTimeout(options.signal, timeoutMs);
  try {
    const response = await fetch(`${controlplaneBaseURL}${requestPath}`, {
      method,
      headers,
      credentials: options.credentials ?? "same-origin",
      body:
        options.serializedBody !== undefined
          ? options.serializedBody
          : options.body === undefined
            ? undefined
            : JSON.stringify(options.body),
      signal: timeout.signal,
      cache: options.cache,
    });

    if (!response.ok) {
      if (response.status === 401 && typeof window !== "undefined" && !requestPath.includes("/auth/login")) {
        window.dispatchEvent(new CustomEvent("iam:unauthorized"));
      }
      throw new APIError(
        response.status,
        await responseMessage(response),
        response.headers.get("x-request-id") ?? undefined,
      );
    }

    if (response.status === 204) return undefined as T;
    const text = await response.text();
    if (!text.trim()) return undefined as T;
    const stripped = text.startsWith(")]}',\n")
      ? text.slice(6)
      : text.startsWith(")]}',")
        ? text.slice(5)
        : text;
    return JSON.parse(stripped) as T;
  } catch (error) {
    if (timeout.signal.aborted && !options.signal?.aborted) {
      throw new APIError(408, "Request timed out.");
    }
    if (error instanceof TypeError && !options.signal?.aborted) {
      throw new APIError(0, "Network request failed.");
    }
    throw error;
  } finally {
    timeout.cancel();
  }
}
