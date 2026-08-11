export type GatewayObject = {
  key: string;
  size: number;
  last_modified: string;
};

export type GatewayObjectHead = {
  contentType?: string;
  etag?: string;
  customMetadata: Record<string, string>;
  versionId?: string;
};

export class StorageGatewayError extends Error {
  readonly status: number;
  readonly code: "forbidden" | "preparing" | "unavailable" | "invalid";

  constructor(status: number, message: string, code: StorageGatewayError["code"]) {
    super(message);
    this.name = "StorageGatewayError";
    this.status = status;
    this.code = code;
  }
}

export type StorageTransferOperation = "upload" | "download";

export type StorageDataTicket = {
  method: "PUT" | "GET";
  url: string;
  transfer_ticket: string;
  expires_at_unix_seconds: number;
};

function encodePathSegment(value: string): string {
  if (!value || value === "." || value === ".." || value.includes("\\") || value.includes("\0")) {
    throw new StorageGatewayError(400, "Invalid object key.", "invalid");
  }
  return encodeURIComponent(value);
}

function objectPath(bucketName: string, objectKey?: string): string {
  const bucket = encodePathSegment(bucketName);
  const key = objectKey
    ? `/objects/${objectKey.split("/").map(encodePathSegment).join("/")}`
    : "/objects";
  return `/zone-control/v1/storage/buckets/${bucket}${key}`;
}

function assertSafeBucketName(bucketName: string): void {
  if (!/^[a-z0-9][a-z0-9.-]{1,62}$/.test(bucketName)) {
    throw new StorageGatewayError(400, "Invalid bucket name.", "invalid");
  }
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const timeout = window.setTimeout(resolve, ms);
    const abort = () => {
      window.clearTimeout(timeout);
      reject(signal?.reason ?? new DOMException("The operation was aborted.", "AbortError"));
    };
    if (!signal) return;
    const cleanup = () => signal.removeEventListener("abort", abort);
    if (signal.aborted) {
      abort();
      return;
    }
    signal.addEventListener("abort", abort, { once: true });
    window.setTimeout(cleanup, ms);
  });
}

async function gatewayRequest(
  path: string,
  accessSessionId: string,
  options: {
    method?: "GET" | "HEAD" | "PUT" | "POST" | "DELETE";
    body?: string;
    contentType?: string;
    signal?: AbortSignal;
  } = {},
): Promise<Response> {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort("storage gateway timeout"), 15_000);
  const abort = () => controller.abort(options.signal?.reason);
  if (options.signal?.aborted) controller.abort(options.signal.reason);
  else options.signal?.addEventListener("abort", abort, { once: true });
  try {
    const response = await fetch(path, {
      method: options.method ?? "GET",
      credentials: "same-origin",
      headers: {
        "x-aurora-access-session-id": accessSessionId,
        ...(options.body ? { "content-type": options.contentType ?? "application/xml" } : {}),
      },
      body: options.body,
      signal: controller.signal,
    });
    if (!response.ok) {
      const code = response.status === 403 ? "forbidden" : response.status === 404 || response.status >= 500 ? "unavailable" : "invalid";
      throw new StorageGatewayError(
        response.status,
        code === "forbidden"
          ? "Storage access is not ready or the operation is forbidden."
          : code === "unavailable"
            ? "Zone Control Edge Gateway is unavailable."
            : "Zone Control Edge Gateway rejected the request.",
        code,
      );
    }
    return response;
  } finally {
    window.clearTimeout(timeout);
    options.signal?.removeEventListener("abort", abort);
  }
}

function xmlDocument(text: string): Document {
  const document = new DOMParser().parseFromString(text, "application/xml");
  if (document.querySelector("parsererror")) throw new StorageGatewayError(502, "Invalid object response.", "invalid");
  return document;
}

export async function listGatewayObjects(
  bucketName: string,
  accessSessionId: string,
  signal?: AbortSignal,
): Promise<GatewayObject[]> {
  assertSafeBucketName(bucketName);
  const objects: GatewayObject[] = [];
  let continuation: string | undefined;
  for (let page = 0; page < 100; page += 1) {
    const query = new URLSearchParams({ "list-type": "2", "max-keys": "1000" });
    if (continuation) query.set("continuation-token", continuation);
    const response = await gatewayRequest(`${objectPath(bucketName)}?${query}`, accessSessionId, { signal });
    const document = xmlDocument(await response.text());
    for (const node of Array.from(document.getElementsByTagNameNS("*", "Contents"))) {
      const key = node.getElementsByTagNameNS("*", "Key")[0]?.textContent ?? "";
      const size = Number(node.getElementsByTagNameNS("*", "Size")[0]?.textContent ?? 0);
      const modified = node.getElementsByTagNameNS("*", "LastModified")[0]?.textContent ?? "";
      if (key && Number.isFinite(size) && size >= 0) objects.push({ key, size, last_modified: modified });
    }
    const truncated = document.getElementsByTagNameNS("*", "IsTruncated")[0]?.textContent === "true";
    continuation = document.getElementsByTagNameNS("*", "NextContinuationToken")[0]?.textContent || undefined;
    if (!truncated || !continuation) return objects;
  }
  throw new StorageGatewayError(413, "Too many objects; narrow the object scope before listing.", "invalid");
}

export async function headGatewayObject(bucketName: string, objectKey: string, accessSessionId: string, signal?: AbortSignal): Promise<GatewayObjectHead> {
  const response = await gatewayRequest(objectPath(bucketName, objectKey), accessSessionId, { method: "HEAD", signal });
  const customMetadata: Record<string, string> = {};
  response.headers.forEach((value, key) => {
    if (key.startsWith("x-amz-meta-")) customMetadata[key.slice("x-amz-meta-".length)] = value;
  });
  return {
    contentType: response.headers.get("content-type") ?? undefined,
    etag: response.headers.get("etag") ?? undefined,
    customMetadata,
    versionId: response.headers.get("x-amz-version-id") ?? undefined,
  };
}

export async function getGatewayTags(bucketName: string, objectKey: string, accessSessionId: string, signal?: AbortSignal): Promise<Record<string, string>> {
  const response = await gatewayRequest(`${objectPath(bucketName, objectKey)}/tags`, accessSessionId, { signal });
  const document = xmlDocument(await response.text());
  const tags: Record<string, string> = {};
  for (const tag of Array.from(document.getElementsByTagNameNS("*", "Tag"))) {
    const key = tag.getElementsByTagNameNS("*", "Key")[0]?.textContent;
    const value = tag.getElementsByTagNameNS("*", "Value")[0]?.textContent;
    if (key) tags[key] = value ?? "";
  }
  return tags;
}

export async function putGatewayTags(bucketName: string, objectKey: string, tags: Record<string, string>, accessSessionId: string, signal?: AbortSignal): Promise<void> {
  const entries = Object.entries(tags);
  if (entries.length > 10 || entries.some(([key, value]) => key.length > 128 || value.length > 256)) {
    throw new StorageGatewayError(400, "Object tags exceed the allowed size.", "invalid");
  }
  const body = `<Tagging><TagSet>${entries.map(([key, value]) => `<Tag><Key>${escapeXml(key)}</Key><Value>${escapeXml(value)}</Value></Tag>`).join("")}</TagSet></Tagging>`;
  await gatewayRequest(`${objectPath(bucketName, objectKey)}/tags`, accessSessionId, { method: "PUT", body, signal });
}

export async function bulkDeleteGatewayObjects(bucketName: string, keys: string[], accessSessionId: string, signal?: AbortSignal): Promise<void> {
  if (keys.length === 0 || keys.length > 1_000) throw new StorageGatewayError(400, "Select between 1 and 1000 objects.", "invalid");
  const body = `<Delete>${keys.map((key) => `<Object><Key>${escapeXml(key)}</Key></Object>`).join("")}</Delete>`;
  assertSafeBucketName(bucketName);
  await gatewayRequest(`/zone-control/v1/storage/buckets/${encodePathSegment(bucketName)}/bulk-delete`, accessSessionId, { method: "POST", body, signal });
}

function escapeXml(value: string): string {
  return value.replace(/[<>&'\"]/g, (character) => ({ "<": "&lt;", ">": "&gt;", "&": "&amp;", "'": "&apos;", '"': "&quot;" })[character] ?? character);
}

export async function requestDataTicket(
  accessSessionId: string,
  request: {
    operation: StorageTransferOperation;
    bucketName: string;
    objectKey: string;
    contentLength?: number;
    contentType?: string;
  },
  signal?: AbortSignal,
): Promise<StorageDataTicket> {
  assertSafeBucketName(request.bucketName);
  const ticketResponse = await gatewayRequest("/zone-control/v1/transfer-tickets", accessSessionId, {
    method: "POST",
    contentType: "application/json",
    body: JSON.stringify({
      capability: "storage.object",
      operation: request.operation,
      access_session_id: accessSessionId,
      resource: {
        bucket_name: request.bucketName,
        object_key: request.objectKey,
      },
      constraints: {
        ...(request.contentLength === undefined ? {} : { content_length: request.contentLength }),
        ...(request.contentType ? { content_type: request.contentType } : {}),
      },
    }),
    signal,
  });
  const parsed: unknown = await ticketResponse.json();
  if (!parsed || typeof parsed !== "object") {
    throw new StorageGatewayError(502, "Zone transfer ticket response is invalid.", "invalid");
  }
  const value = parsed as Partial<StorageDataTicket>;
  if (
    (value.method !== "PUT" && value.method !== "GET") ||
    typeof value.url !== "string" ||
    typeof value.transfer_ticket !== "string" ||
    !value.transfer_ticket.includes(".") ||
    typeof value.expires_at_unix_seconds !== "number" ||
    value.expires_at_unix_seconds <= Math.floor(Date.now() / 1000)
  ) {
    throw new StorageGatewayError(502, "Zone transfer ticket response is invalid.", "invalid");
  }
  let ticketUrl: URL;
  try {
    ticketUrl = new URL(value.url, window.location.origin);
  } catch {
    throw new StorageGatewayError(502, "Zone transfer ticket URL is invalid.", "invalid");
  }
  if (ticketUrl.protocol !== "https:" && ticketUrl.protocol !== "http:") {
    throw new StorageGatewayError(502, "Zone transfer ticket URL is invalid.", "invalid");
  }
  return {
    method: value.method,
    url: ticketUrl.toString(),
    transfer_ticket: value.transfer_ticket,
    expires_at_unix_seconds: value.expires_at_unix_seconds,
  };
}

async function transferRequest(
  ticket: StorageDataTicket,
  body: BodyInit | null,
  contentType: string | undefined,
  signal?: AbortSignal,
): Promise<Response> {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort("storage transfer timeout"), 15 * 60_000);
  const abort = () => controller.abort(signal?.reason);
  if (signal?.aborted) controller.abort(signal.reason);
  else signal?.addEventListener("abort", abort, { once: true });
  try {
    const response = await fetch(ticket.url, {
      method: ticket.method,
      credentials: "omit",
      headers: {
        "x-aurora-transfer-ticket": ticket.transfer_ticket,
        ...(contentType ? { "content-type": contentType } : {}),
      },
      body,
      signal: controller.signal,
    });
    if (!response.ok) {
      const code = response.status === 403 ? "forbidden" : response.status >= 500 ? "unavailable" : "invalid";
      throw new StorageGatewayError(
        response.status,
        code === "forbidden"
          ? "The transfer ticket is expired, already used, or forbidden."
          : code === "unavailable"
            ? "Zone Public Edge Gateway is unavailable."
            : "Zone Public Edge Gateway rejected the transfer.",
        code,
      );
    }
    return response;
  } finally {
    window.clearTimeout(timeout);
    signal?.removeEventListener("abort", abort);
  }
}

export async function uploadGatewayObject(
  bucketName: string,
  objectKey: string,
  file: File,
  accessSessionId: string,
  signal?: AbortSignal,
): Promise<void> {
  const contentType = file.type || "application/octet-stream";
  const ticket = await requestDataTicket(accessSessionId, {
    operation: "upload",
    bucketName,
    objectKey,
    contentLength: file.size,
    contentType,
  }, signal);
  if (ticket.method !== "PUT") throw new StorageGatewayError(502, "Upload ticket method is invalid.", "invalid");
  await transferRequest(ticket, file, contentType, signal);
}

export async function downloadGatewayObject(
  bucketName: string,
  objectKey: string,
  accessSessionId: string,
  signal?: AbortSignal,
): Promise<Blob> {
  const ticket = await requestDataTicket(accessSessionId, {
    operation: "download",
    bucketName,
    objectKey,
  }, signal);
  if (ticket.method !== "GET") throw new StorageGatewayError(502, "Download ticket method is invalid.", "invalid");
  const response = await transferRequest(ticket, null, undefined, signal);
  return response.blob();
}

export { sleep };
