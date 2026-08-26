export type GatewayObject = {
  key: string;
  size: number;
  last_modified: string;
};

export type GatewayObjectVersion = {
  versionId: string;
  isLatest: boolean;
  isDeleteMarker: boolean;
  lastModified: string;
  size: number;
  etag: string;
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

export type StorageTransferOperation =
  | "upload"
  | "download"
  | "multipart_initiate"
  | "multipart_upload_part"
  | "multipart_complete"
  | "multipart_abort";

export type StorageDataTicket = {
  method: "PUT" | "GET" | "POST" | "DELETE";
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

export async function listGatewayObjectVersions(
  bucketName: string,
  objectKey: string,
  accessSessionId: string,
  signal?: AbortSignal,
): Promise<GatewayObjectVersion[]> {
  assertSafeBucketName(bucketName);
  const versions: GatewayObjectVersion[] = [];
  let keyMarker: string | undefined;
  let versionIdMarker: string | undefined;

  for (let page = 0; page < 100; page += 1) {
    const query = new URLSearchParams({
      versions: "",
      prefix: objectKey,
      "max-keys": "1000",
    });
    if (keyMarker) query.set("key-marker", keyMarker);
    if (versionIdMarker) query.set("version-id-marker", versionIdMarker);

    const response = await gatewayRequest(`${objectPath(bucketName)}?${query}`, accessSessionId, { signal });
    const document = xmlDocument(await response.text());

    // Parse <Version> elements
    for (const node of Array.from(document.getElementsByTagNameNS("*", "Version"))) {
      const key = node.getElementsByTagNameNS("*", "Key")[0]?.textContent ?? "";
      if (key !== objectKey) continue;
      const versionId = node.getElementsByTagNameNS("*", "VersionId")[0]?.textContent ?? "";
      const isLatest = node.getElementsByTagNameNS("*", "IsLatest")[0]?.textContent === "true";
      const lastModified = node.getElementsByTagNameNS("*", "LastModified")[0]?.textContent ?? "";
      const size = Number(node.getElementsByTagNameNS("*", "Size")[0]?.textContent ?? 0);
      const etag = (node.getElementsByTagNameNS("*", "ETag")[0]?.textContent ?? "").replace(/"/g, "");

      versions.push({
        versionId,
        isLatest,
        isDeleteMarker: false,
        lastModified,
        size,
        etag,
      });
    }

    // Parse <DeleteMarker> elements
    for (const node of Array.from(document.getElementsByTagNameNS("*", "DeleteMarker"))) {
      const key = node.getElementsByTagNameNS("*", "Key")[0]?.textContent ?? "";
      if (key !== objectKey) continue;
      const versionId = node.getElementsByTagNameNS("*", "VersionId")[0]?.textContent ?? "";
      const isLatest = node.getElementsByTagNameNS("*", "IsLatest")[0]?.textContent === "true";
      const lastModified = node.getElementsByTagNameNS("*", "LastModified")[0]?.textContent ?? "";

      versions.push({
        versionId,
        isLatest,
        isDeleteMarker: true,
        lastModified,
        size: 0,
        etag: "",
      });
    }

    const truncated = document.getElementsByTagNameNS("*", "IsTruncated")[0]?.textContent === "true";
    keyMarker = document.getElementsByTagNameNS("*", "NextKeyMarker")[0]?.textContent || undefined;
    versionIdMarker = document.getElementsByTagNameNS("*", "NextVersionIdMarker")[0]?.textContent || undefined;
    if (!truncated || (!keyMarker && !versionIdMarker)) return versions;
  }
  return versions;
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
    uploadId?: string;
    partNumber?: number;
    versionId?: string;
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
        ...(request.uploadId ? { upload_id: request.uploadId } : {}),
        ...(request.partNumber !== undefined ? { part_number: request.partNumber } : {}),
        ...(request.versionId ? { version_id: request.versionId } : {}),
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
    (value.method !== "PUT" && value.method !== "GET" && value.method !== "POST" && value.method !== "DELETE") ||
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
  // Zone Control is configured with an HTTPS-only public base URL. Refuse a
  // downgraded or arbitrary scheme before attaching the one-time ticket.
  if (ticketUrl.protocol !== "https:") {
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

export async function downloadGatewayObjectVersion(
  bucketName: string,
  objectKey: string,
  versionId: string,
  accessSessionId: string,
  signal?: AbortSignal,
): Promise<Blob> {
  const ticket = await requestDataTicket(accessSessionId, {
    operation: "download",
    bucketName,
    objectKey,
    versionId,
  }, signal);
  if (ticket.method !== "GET") throw new StorageGatewayError(502, "Download ticket method is invalid.", "invalid");
  const response = await transferRequest(ticket, null, undefined, signal);
  return response.blob();
}

export async function deleteGatewayObjectVersion(
  bucketName: string,
  objectKey: string,
  versionId: string,
  accessSessionId: string,
  signal?: AbortSignal,
): Promise<void> {
  assertSafeBucketName(bucketName);
  await gatewayRequest(
    `${objectPath(bucketName, objectKey)}?versionId=${encodeURIComponent(versionId)}`,
    accessSessionId,
    { method: "DELETE", signal },
  );
}

export async function restoreGatewayObjectVersion(
  bucketName: string,
  objectKey: string,
  versionId: string,
  accessSessionId: string,
  signal?: AbortSignal,
): Promise<void> {
  const blob = await downloadGatewayObjectVersion(bucketName, objectKey, versionId, accessSessionId, signal);
  const file = new File([blob], objectKey.split("/").pop() || "restored-object", { type: blob.type });
  await uploadGatewayObject(bucketName, objectKey, file, accessSessionId, signal);
}

export async function initiateMultipartUpload(
  bucketName: string,
  objectKey: string,
  accessSessionId: string,
  contentType?: string,
  signal?: AbortSignal,
): Promise<string> {
  const mime = contentType || "application/octet-stream";
  const ticket = await requestDataTicket(
    accessSessionId,
    {
      operation: "multipart_initiate",
      bucketName,
      objectKey,
      contentType: mime,
    },
    signal,
  );
  if (ticket.method !== "POST") throw new StorageGatewayError(502, "Initiate ticket method is invalid.", "invalid");
  const response = await transferRequest(ticket, null, mime, signal);
  const text = await response.text();
  const document = xmlDocument(text);
  const uploadId = document.getElementsByTagNameNS("*", "UploadId")[0]?.textContent;
  if (!uploadId) {
    throw new StorageGatewayError(502, "Failed to parse S3 UploadId from initiate response.", "invalid");
  }
  return uploadId;
}

export async function uploadPart(
  bucketName: string,
  objectKey: string,
  accessSessionId: string,
  uploadId: string,
  partNumber: number,
  chunk: Blob,
  signal?: AbortSignal,
): Promise<string> {
  const ticket = await requestDataTicket(
    accessSessionId,
    {
      operation: "multipart_upload_part",
      bucketName,
      objectKey,
      uploadId,
      partNumber,
      contentLength: chunk.size,
    },
    signal,
  );
  if (ticket.method !== "PUT") throw new StorageGatewayError(502, "Upload part ticket method is invalid.", "invalid");
  const response = await transferRequest(ticket, chunk, undefined, signal);
  const etag = response.headers.get("etag") || response.headers.get("ETag");
  if (!etag) {
    throw new StorageGatewayError(502, `Missing ETag for part ${partNumber}.`, "invalid");
  }
  return etag.replace(/^"(.*)"$/, "$1");
}

export async function completeMultipartUpload(
  bucketName: string,
  objectKey: string,
  accessSessionId: string,
  uploadId: string,
  parts: { partNumber: number; etag: string }[],
  signal?: AbortSignal,
): Promise<void> {
  const sorted = [...parts].sort((a, b) => a.partNumber - b.partNumber);
  const xmlBody = `<CompleteMultipartUpload>${sorted
    .map((p) => `<Part><PartNumber>${p.partNumber}</PartNumber><ETag>"${p.etag.replace(/"/g, "")}"</ETag></Part>`)
    .join("")}</CompleteMultipartUpload>`;
  const ticket = await requestDataTicket(
    accessSessionId,
    {
      operation: "multipart_complete",
      bucketName,
      objectKey,
      uploadId,
      contentLength: new Blob([xmlBody]).size,
      contentType: "application/xml",
    },
    signal,
  );
  if (ticket.method !== "POST") throw new StorageGatewayError(502, "Complete ticket method is invalid.", "invalid");
  await transferRequest(ticket, xmlBody, "application/xml", signal);
}

export async function abortMultipartUpload(
  bucketName: string,
  objectKey: string,
  accessSessionId: string,
  uploadId: string,
  signal?: AbortSignal,
): Promise<void> {
  try {
    const ticket = await requestDataTicket(
      accessSessionId,
      {
        operation: "multipart_abort",
        bucketName,
        objectKey,
        uploadId,
      },
      signal,
    );
    if (ticket.method === "DELETE") {
      await transferRequest(ticket, null, undefined, signal);
    }
  } catch (error) {
    console.warn("Failed to abort multipart upload:", error);
  }
}

export type MultipartProgress = {
  loaded: number;
  total: number;
  partIndex: number;
  totalParts: number;
  speedBytesPerSec?: number;
};

export async function uploadLargeFile(
  bucketName: string,
  objectKey: string,
  accessSessionId: string,
  file: File,
  options: {
    chunkSize?: number;
    concurrency?: number;
    onProgress?: (progress: MultipartProgress) => void;
    signal?: AbortSignal;
  } = {},
): Promise<void> {
  const minChunk = 5 * 1024 * 1024;
  let chunkSize = options.chunkSize || 10 * 1024 * 1024;
  if (file.size / chunkSize > 9900) {
    chunkSize = Math.ceil(file.size / 9900);
  }
  chunkSize = Math.max(chunkSize, minChunk);

  const totalParts = Math.ceil(file.size / chunkSize);
  if (totalParts <= 1) {
    return uploadGatewayObject(bucketName, objectKey, file, accessSessionId, options.signal);
  }

  const contentType = file.type || "application/octet-stream";
  const uploadId = await initiateMultipartUpload(bucketName, objectKey, accessSessionId, contentType, options.signal);

  const parts: { partNumber: number; etag: string }[] = [];
  const partSizes = new Array<number>(totalParts).fill(0);
  const partBytesUploaded = new Array<number>(totalParts).fill(0);
  let completedPartsCount = 0;
  const startTime = Date.now();

  const updateProgress = () => {
    if (!options.onProgress) return;
    const loaded = partBytesUploaded.reduce((sum, bytes) => sum + bytes, 0);
    const elapsedSec = (Date.now() - startTime) / 1000;
    const speedBytesPerSec = elapsedSec > 0.5 ? loaded / elapsedSec : 0;
    options.onProgress({
      loaded,
      total: file.size,
      partIndex: completedPartsCount,
      totalParts,
      speedBytesPerSec,
    });
  };

  const concurrency = Math.min(options.concurrency || 3, 5);
  let nextIndex = 0;
  let hasError: unknown = null;

  async function worker() {
    while (nextIndex < totalParts && !hasError && !options.signal?.aborted) {
      const index = nextIndex++;
      const partNumber = index + 1;
      const start = index * chunkSize;
      const end = Math.min(start + chunkSize, file.size);
      const chunk = file.slice(start, end);
      partSizes[index] = chunk.size;

      let partSuccess = false;
      let lastErr: unknown = null;

      for (let attempt = 1; attempt <= 3 && !partSuccess && !options.signal?.aborted; attempt++) {
        try {
          const etag = await uploadPart(bucketName, objectKey, accessSessionId, uploadId, partNumber, chunk, options.signal);
          parts.push({ partNumber, etag });
          partBytesUploaded[index] = chunk.size;
          completedPartsCount++;
          updateProgress();
          partSuccess = true;
        } catch (err) {
          lastErr = err;
          if (attempt < 3 && !options.signal?.aborted) {
            await sleep(attempt * 500, options.signal);
          }
        }
      }

      if (!partSuccess) {
        hasError = lastErr || new Error(`Upload part ${partNumber} failed after 3 attempts.`);
        return;
      }
    }
  }

  try {
    const workers = Array.from({ length: concurrency }, () => worker());
    await Promise.all(workers);

    if (hasError) {
      throw hasError;
    }
    if (options.signal?.aborted) {
      throw options.signal.reason || new Error("Upload aborted");
    }

    await completeMultipartUpload(bucketName, objectKey, accessSessionId, uploadId, parts, options.signal);
  } catch (error) {
    void abortMultipartUpload(bucketName, objectKey, accessSessionId, uploadId);
    throw error;
  }
}

export { sleep };
