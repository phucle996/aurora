import { fetchJSON } from "@/shared/api/http";
import { criticalFetchJSON } from "@/shared/api/critical";

function pathSegment(value: string): string {
  if (!value || value.includes("/") || value.includes("\\") || value.includes("\0")) {
    throw new Error("Invalid storage resource identifier.");
  }
  return encodeURIComponent(value);
}

export type BucketLifecycleRule = {
  id: string;
  enabled: boolean;
  prefix: string;
  expiration_days: number;
  noncurrent_version_expiration_days: number;
  abort_incomplete_multipart_upload_days: number;
};

// [COMMENT]: BucketItem đại diện cho thông tin một Bucket được trả về từ GET/LIST API.
// Đã đồng bộ theo cấu trúc JSON snake_case thực tế của Backend.
export type BucketItem = {
  id: string;
  name: string;
  workspace_id: string;
  // [COMMENT]: status đã bị bỏ — bucket tồn tại trong DB là đủ để xác định active
  capacity_quota_bytes: number;
  used_mb?: string; // Dung lượng thực tế cho UI, fixed-point decimal MB
  versioning_enabled?: boolean;
  lifecycle_rules?: BucketLifecycleRule[];
  created_at: string;
  updated_at: string;
};

// [COMMENT]: CreatedBucketResult chứa thông tin bucket và credential thô vừa được khởi tạo.
// ⚠ Lưu ý: Sử dụng DTO ở backend nên các key dùng snake_case chữ thường.
export type CreatedBucketResult = {
  bucket_id: string;
  bucket_name: string;
  credential_id: string;
  access_key: string;
  secret_key: string; // Plaintext, chỉ trả về 1 lần duy nhất
  policy: string;
};

// [COMMENT]: CredentialItem đại diện cho Access Key của Bucket.
// ⚠ Lưu ý: Dùng DTO ở backend nên các key dùng snake_case chữ thường.
export type CredentialItem = {
  id: string;
  // [COMMENT]: Làm cho bucket_id là trường tùy chọn vì API list credentials đã tối ưu không trả về
  bucket_id?: string;
  access_key: string;
  secret_key?: string; // Chỉ xuất hiện khi tạo mới
  policy: string;
  created_at: string;
  updated_at: string;
};

// [COMMENT]: Lấy danh sách các buckets thuộc Workspace hiện tại (Personal) hoặc Tenant
export async function listBuckets(signal?: AbortSignal): Promise<BucketItem[]> {
  const res = await fetchJSON<{ data?: BucketItem[] }>("/api/v1/storage/buckets", {
    method: "GET",
    signal,
  });
  return res?.data || [];
}

// [COMMENT]: Khởi tạo một storage bucket mới
export async function createBucket(
  name: string,
  quotaBytes: number,
  policy: string,
  advancedOptions: {
    encrypt_enabled: boolean;
    versioning_enabled: boolean;
    object_locking_enabled: boolean;
    replication_enabled: boolean;
    retention_days: number;
    legal_hold_enabled: boolean;
    tags: Record<string, string>;
  },
  signal?: AbortSignal
): Promise<CreatedBucketResult> {
  const res = await criticalFetchJSON<{ data?: CreatedBucketResult }>("/api/v1/critical/storage/buckets", {
    method: "POST",
    body: {
      name,
      quota_bytes: quotaBytes,
      policy,
      ...advancedOptions,
    },
    signal,
  });
  if (!res?.data) {
    throw new Error("Failed to read created bucket data");
  }
  return res.data;
}

// [COMMENT]: Xem thông tin chi tiết một bucket
export async function getBucketDetails(
  id: string,
  signal?: AbortSignal
): Promise<BucketItem> {
  const res = await fetchJSON<{ data?: BucketItem }>(`/api/v1/storage/buckets/${pathSegment(id)}`, {
    method: "GET",
    signal,
  });
  if (!res?.data) {
    throw new Error("Bucket details not found");
  }
  return res.data;
}

// [COMMENT]: Cập nhật dung lượng hạn mức (Quota) của bucket
export async function updateBucketQuota(
  id: string,
  quotaBytes: number,
  signal?: AbortSignal
): Promise<void> {
  await criticalFetchJSON(`/api/v1/critical/storage/buckets/${pathSegment(id)}/quota`, {
    method: "PATCH",
    body: {
      quota_bytes: quotaBytes,
    },
    signal,
  });
}



// [COMMENT]: Yêu cầu xóa bucket
export async function deleteBucket(
  id: string,
  signal?: AbortSignal
): Promise<void> {
  await criticalFetchJSON(`/api/v1/critical/storage/buckets/${pathSegment(id)}`, {
    method: "DELETE",
    signal,
  });
}

// [COMMENT]: Liệt kê các Access Keys của bucket cụ thể
export async function listCredentials(
  bucketID: string,
  signal?: AbortSignal
): Promise<CredentialItem[]> {
  const res = await fetchJSON<{ data?: CredentialItem[] }>(
    `/api/v1/storage/buckets/${pathSegment(bucketID)}/credentials`,
    {
      method: "GET",
      signal,
    }
  );
  return res?.data || [];
}

// [COMMENT]: Sinh thêm Access Key mới cho bucket
export async function createCredential(
  bucketName: string,
  policy: string,
  signal?: AbortSignal
): Promise<CredentialItem> {
  const res = await criticalFetchJSON<{ data?: CredentialItem }>(
    `/api/v1/critical/storage/buckets/${pathSegment(bucketName)}/credentials`,
    {
      method: "POST",
      body: {
        policy,
      },
      signal,
    }
  );
  if (!res?.data) {
    throw new Error("Failed to generate access key");
  }
  return res.data;
}

// [COMMENT]: Xóa bỏ Access Key — cần truyền access_key trong body để backend gửi lệnh xóa tới MinIO mà không cần DB lookup thêm.
export async function deleteCredential(
  bucketId: string,
  credentialID: string,
  accessKey: string,
  signal?: AbortSignal
): Promise<void> {
  await criticalFetchJSON(`/api/v1/critical/storage/buckets/${pathSegment(bucketId)}/credentials/${pathSegment(credentialID)}`, {
    method: "DELETE",
    body: { access_key: accessKey },
    signal,
  });
}

// [COMMENT]: Liệt kê danh sách tên vật lý của tất cả các buckets thuộc Workspace hiện tại (Personal) - Lightweight API.
export async function listBucketNames(signal?: AbortSignal): Promise<string[]> {
  const res = await fetchJSON<{ data?: string[] }>("/api/v1/storage/buckets/names", {
    method: "GET",
    signal,
  });
  return res?.data || [];
}

export type StorageAccessSession = {
  access_session_id: string;
  zone_id: string;
  bucket_id: string;
  expires_at: string;
  gateway_path: string;
};

export type StorageAccessSessionReadiness = {
  access_session_id: string;
  bucket_id: string;
  status: "PENDING" | "ACTIVE" | "FAILED";
  completed_at?: string;
  error_code?: string;
};

export async function createStorageAccessSession(
  bucketId: string,
  request: { durationSeconds: number; actions: string[]; keyPrefix?: string },
  signal?: AbortSignal,
): Promise<StorageAccessSession> {
  const response = await fetchJSON<{ data?: StorageAccessSession }>(
    `/api/v1/storage/buckets/${pathSegment(bucketId)}/access-sessions`,
    {
      method: "POST",
      body: {
        duration_seconds: request.durationSeconds,
        actions: request.actions,
        key_prefix: request.keyPrefix ?? "",
      },
      signal,
    },
  );
  const data = response.data;
  if (!data || typeof data.access_session_id !== "string" || data.access_session_id.length < 8 || data.access_session_id.length > 256 ||
    data.bucket_id !== bucketId || typeof data.zone_id !== "string" || typeof data.expires_at !== "string" ||
    typeof data.gateway_path !== "string" || !data.gateway_path.startsWith("/")) {
    throw new Error("Storage access session response is invalid.");
  }
  return data;
}

export async function getStorageAccessSessionReadiness(
  bucketId: string,
  accessSessionId: string,
  signal?: AbortSignal,
): Promise<StorageAccessSessionReadiness> {
  const response = await fetchJSON<{ data?: StorageAccessSessionReadiness }>(
    `/api/v1/storage/buckets/${pathSegment(bucketId)}/access-sessions/${pathSegment(accessSessionId)}`,
    { method: "GET", signal, cache: "no-store" },
  );
  const data = response.data;
  if (!data || data.bucket_id !== bucketId || data.access_session_id !== accessSessionId ||
    !["PENDING", "ACTIVE", "FAILED"].includes(data.status)) {
    throw new Error("Storage access session readiness response is invalid.");
  }
  return data;
}

export async function updateBucketVersioning(
  bucketId: string,
  versioningEnabled: boolean,
  signal?: AbortSignal,
): Promise<{ id: string; name: string; versioning_enabled: boolean }> {
  const res = await criticalFetchJSON<{ data?: { id: string; name: string; versioning_enabled: boolean } }>(
    `/api/v1/critical/storage/buckets/${pathSegment(bucketId)}/versioning`,
    {
      method: "PATCH",
      body: {
        versioning_enabled: versioningEnabled,
      },
      signal,
    },
  );
  if (!res?.data) {
    throw new Error("Failed to update bucket versioning");
  }
  return res.data;
}

export async function getBucketLifecycle(
  bucketId: string,
  signal?: AbortSignal,
): Promise<BucketLifecycleRule[]> {
  const res = await fetchJSON<{ data?: { rules?: BucketLifecycleRule[] } }>(
    `/api/v1/storage/buckets/${pathSegment(bucketId)}/lifecycle`,
    {
      method: "GET",
      signal,
    },
  );
  return res?.data?.rules || [];
}

export async function updateBucketLifecycle(
  bucketId: string,
  rules: BucketLifecycleRule[],
  signal?: AbortSignal,
): Promise<{ id: string; name: string; lifecycle_rules: BucketLifecycleRule[] }> {
  const res = await criticalFetchJSON<{ data?: { id: string; name: string; lifecycle_rules: BucketLifecycleRule[] } }>(
    `/api/v1/critical/storage/buckets/${pathSegment(bucketId)}/lifecycle`,
    {
      method: "PUT",
      body: {
        rules,
      },
      signal,
    },
  );
  if (!res?.data) {
    throw new Error("Failed to update bucket lifecycle rules");
  }
  return res.data;
}
