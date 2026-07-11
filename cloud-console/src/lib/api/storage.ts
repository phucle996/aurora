import { fetchJSON } from "./fetcher";

// [COMMENT]: BucketItem đại diện cho thông tin một Bucket được trả về từ GET/LIST API.
// ⚠ Lưu ý: Do Backend trả về Entity trực tiếp không qua DTO nên các key bắt đầu bằng chữ HOA.
export type BucketItem = {
  ID: string;
  Name: string;
  WorkspaceID: string;
  ZoneID: string;
  TenantID?: string; // Chỉ xuất hiện với Tenant Bucket
  Status: "creating" | "active" | "suspended" | "deleted";
  CapacityQuotaBytes: number;
  CreatedAt: string;
  UpdatedAt: string;
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
  bucket_id: string;
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
  zoneID: string,
  signal?: AbortSignal
): Promise<CreatedBucketResult> {
  const res = await fetchJSON<{ data?: CreatedBucketResult }>("/api/v1/storage/buckets", {
    method: "POST",
    headers: {
      "X-Zone-ID": zoneID,
    },
    body: {
      name,
      quota_bytes: quotaBytes,
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
  const res = await fetchJSON<{ data?: BucketItem }>(`/api/v1/storage/buckets/${id}`, {
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
  await fetchJSON(`/api/v1/storage/buckets/${id}/quota`, {
    method: "PATCH",
    body: {
      quota_bytes: quotaBytes,
    },
    signal,
  });
}

// [COMMENT]: Tạm đình chỉ hoạt động của bucket
export async function suspendBucket(
  id: string,
  signal?: AbortSignal
): Promise<void> {
  await fetchJSON(`/api/v1/storage/buckets/${id}/suspend`, {
    method: "POST",
    signal,
  });
}

// [COMMENT]: Kích hoạt lại bucket bị suspend
export async function resumeBucket(
  id: string,
  signal?: AbortSignal
): Promise<void> {
  await fetchJSON(`/api/v1/storage/buckets/${id}/resume`, {
    method: "POST",
    signal,
  });
}

// [COMMENT]: Yêu cầu xóa bucket
export async function deleteBucket(
  id: string,
  signal?: AbortSignal
): Promise<void> {
  await fetchJSON(`/api/v1/storage/buckets/${id}`, {
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
    `/api/v1/storage/buckets/${bucketID}/credentials`,
    {
      method: "GET",
      signal,
    }
  );
  return res?.data || [];
}

// [COMMENT]: Sinh thêm Access Key mới cho bucket
export async function createCredential(
  bucketID: string,
  policy: string,
  signal?: AbortSignal
): Promise<CredentialItem> {
  const res = await fetchJSON<{ data?: CredentialItem }>(
    `/api/v1/storage/buckets/${bucketID}/credentials`,
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

// [COMMENT]: Thu hồi / Xóa bỏ Access Key
export async function revokeCredential(
  credentialID: string,
  signal?: AbortSignal
): Promise<void> {
  await fetchJSON(`/api/v1/storage/credentials/${credentialID}`, {
    method: "DELETE",
    signal,
  });
}
