// [COMMENT]: Cache client-side cho Objects Tab sử dụng localStorage.
// Hai loại cache được quản lý:
//   1. object_list:{bucketId}       — JSON snapshot từ pipeline list (TTL 14 phút)
//   2. presign_url:{bucketId}:{key} — Presigned GET URL cho download (TTL 14 phút)
// TTL đặt thấp hơn 1 phút so với server (15 phút) để tránh race condition URL hết hạn.

const TTL_MS = 14 * 60 * 1000; // 14 phút tính theo ms
const SAFE_BUFFER_MS = 60 * 1000; // 60s buffer trước khi hết hạn

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

export interface CachedRawObject {
  key: string;
  size: number;
  last_modified: string;
}

interface CachedListEntry {
  data: CachedRawObject[];
  expires_at: number; // epoch ms
}

interface CachedPresignEntry {
  url: string;
  expires_at: number; // epoch ms
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

function listKey(bucketId: string): string {
  return `object_list:${bucketId}`;
}

function presignKey(bucketId: string, objectKey: string): string {
  // [COMMENT]: objectKey là full S3 path, encode để tránh ký tự đặc biệt trong storage key
  return `presign_url:${bucketId}:${encodeURIComponent(objectKey)}`;
}

function isExpired(entry: { expires_at: number }, bufferMs = 0): boolean {
  return Date.now() > entry.expires_at - bufferMs;
}

function safeGet<T>(key: string): T | null {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return null;
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

function safeSet(key: string, value: unknown): void {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // localStorage có thể full (QuotaExceededError) — silently skip
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Object List Cache
// ─────────────────────────────────────────────────────────────────────────────

/** Đọc danh sách objects từ cache. Trả về null nếu không có hoặc đã hết hạn. */
export function getCachedObjectList(bucketId: string): CachedRawObject[] | null {
  const entry = safeGet<CachedListEntry>(listKey(bucketId));
  if (!entry || isExpired(entry)) {
    localStorage.removeItem(listKey(bucketId));
    return null;
  }
  return entry.data;
}

/** Lưu danh sách objects vào cache với TTL cố định. */
export function setCachedObjectList(bucketId: string, data: CachedRawObject[]): void {
  const entry: CachedListEntry = {
    data,
    expires_at: Date.now() + TTL_MS,
  };
  safeSet(listKey(bucketId), entry);
}

/** Xóa cache danh sách objects khi có thay đổi (upload/delete thành công). */
export function invalidateObjectListCache(bucketId: string): void {
  localStorage.removeItem(listKey(bucketId));
}

// ─────────────────────────────────────────────────────────────────────────────
// Presigned Download URL Cache
// ─────────────────────────────────────────────────────────────────────────────

/** Đọc presigned download URL từ cache. Trả về null nếu không có hoặc sắp hết hạn. */
export function getCachedPresignUrl(bucketId: string, objectKey: string): string | null {
  const entry = safeGet<CachedPresignEntry>(presignKey(bucketId, objectKey));
  // [COMMENT]: Dùng SAFE_BUFFER_MS để tránh trả về URL đang sắp hết hạn (<60s)
  if (!entry || isExpired(entry, SAFE_BUFFER_MS)) {
    localStorage.removeItem(presignKey(bucketId, objectKey));
    return null;
  }
  return entry.url;
}

/** Lưu presigned download URL vào cache với TTL cố định. */
export function setCachedPresignUrl(bucketId: string, objectKey: string, url: string): void {
  const entry: CachedPresignEntry = {
    url,
    expires_at: Date.now() + TTL_MS,
  };
  safeSet(presignKey(bucketId, objectKey), entry);
}
