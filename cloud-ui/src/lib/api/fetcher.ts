import { controlplaneBaseURL } from "./config";

export type APIError = {
  status: number;
  message: string;
};

type FetchJSONOptions = {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  headers?: HeadersInit;
  credentials?: RequestCredentials;
  signal?: AbortSignal;
};

export async function fetchJSON<T>(
  path: string,
  options: FetchJSONOptions = {},
): Promise<T> {
  const {
    method = "GET",
    body,
    headers,
    credentials = "same-origin",
    signal,
  } = options;

  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  const response = await fetch(`${controlplaneBaseURL}${normalizedPath}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(headers || {}),
    },
    credentials,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });

  if (!response.ok) {
    let message = "request failed";
    try {
      const payload = (await response.json()) as { message?: string };
      if (typeof payload?.message === "string" && payload.message.trim() !== "") {
        message = payload.message;
      }
    } catch {
      message = response.statusText || message;
    }

    throw {
      status: response.status,
      message,
    } as APIError;
  }

  if (response.status === 204) {
    return undefined as T;
  }

  return (await response.json()) as T;
}
