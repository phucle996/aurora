import { fetchJSON } from "./fetcher";

// [COMMENT]: BucketItem đại diện cho thông tin một Bucket được trả về từ GET/LIST API.
// Đã đồng bộ theo cấu trúc JSON snake_case thực tế của Backend.
export type BucketItem = {
  id: string;
  name: string;
  workspace_id: string;
  // [COMMENT]: status đã bị bỏ — bucket tồn tại trong DB là đủ để xác định active
  capacity_quota_bytes: number;
  used_bytes?: number; // Dung lượng thực tế đã sử dụng
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
  signal?: AbortSignal
): Promise<CreatedBucketResult> {
  const res = await fetchJSON<{ data?: CreatedBucketResult }>("/api/v1/storage/buckets", {
    method: "POST",
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



// [COMMENT]: Yêu cầu xóa bucket
export async function deleteBucket(
  id: string,
  name: string,
  signal?: AbortSignal
): Promise<void> {
  await fetchJSON(`/api/v1/storage/buckets/${id}?name=${encodeURIComponent(name)}`, {
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
  bucketName: string,
  policy: string,
  signal?: AbortSignal
): Promise<CredentialItem> {
  const res = await fetchJSON<{ data?: CredentialItem }>(
    `/api/v1/storage/buckets/${bucketName}/credentials`,
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
  await fetchJSON(`/api/v1/storage/buckets/${bucketId}/credentials/${credentialID}`, {
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
