import { fetchJSON } from "./fetcher";

// [COMMENT]: getUserDevices lấy danh sách thiết bị thực tế của user phục vụ platform audit
export async function getUserDevices(id: string, signal?: AbortSignal): Promise<{ items: any[]; total: number } | null> {
  try {
    const res = await fetchJSON<{ data?: { items?: any[]; total?: number } }>(`/api/v1/iam/users/${id}/devices`, {
      method: "GET",
      signal,
    });
    return {
      items: res?.data?.items || [],
      total: res?.data?.total || 0,
    };
  } catch (err) {
    console.error("Failed to fetch user devices", err);
    return null;
  }
}
