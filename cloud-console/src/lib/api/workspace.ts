import { fetchJSON } from "./fetcher";

// [COMMENT]: WorkspaceCatalogItem — dữ liệu tối giản từ hot path catalog, khớp với entity Go (id, code, name)
export type WorkspaceCatalogItem = {
  id: string;
  code: string;
  name: string;
};

export type FetchWorkspaceCatalogOptions = {
  zoneID: string;
  userID: string;
  tenantID?: string;    // optional — có thì Tenant context, không có thì Personal
  roleID?: string;      // bắt buộc khi tenantID được cung cấp
  signal?: AbortSignal;
};

// [COMMENT]: fetchWorkspaceCatalog — hot path, trả về danh sách workspace tối giản (id, code, name)
// lọc theo zone + context (personal / tenant). Backend trả về []WorkspaceCatalogItem wrapped trong data field.
export async function fetchWorkspaceCatalog(
  opts: FetchWorkspaceCatalogOptions,
): Promise<WorkspaceCatalogItem[]> {
  const headers: Record<string, string> = {
    "x-user-id": opts.userID,
    "x-zone-id": opts.zoneID,
  };

  // [COMMENT]: Tenant context — inject thêm x-tenant-id và x-user-role-id
  if (opts.tenantID) {
    headers["x-tenant-id"] = opts.tenantID;
    if (opts.roleID) {
      headers["x-user-role-id"] = opts.roleID;
    }
  }

  // [COMMENT]: Gọi hot path, ACR sẽ tự động rewrite URL dựa trên ngữ cảnh gửi kèm trong headers/cookies
  const path = "/api/v1/hierarchy/workspaces/catalog";

  // [COMMENT]: Backend trả về { data: WorkspaceCatalogItem[], message: string, ... }
  const res = await fetchJSON<{ data: WorkspaceCatalogItem[] }>(
    path,
    {
      method: "GET",
      headers,
      signal: opts.signal,
    },
  );

  return res?.data ?? [];
}
