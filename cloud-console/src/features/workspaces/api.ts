import { fetchJSON } from "@/shared/api/http";

// [COMMENT]: WorkspaceCatalogItem — dữ liệu tối giản từ hot path catalog, khớp với entity Go (id, code, name)
export type WorkspaceCatalogItem = {
  id: string;
  code: string;
  name: string;
};

export type FetchWorkspaceCatalogOptions = {
  tenantContext?: boolean;
  signal?: AbortSignal;
};

// [COMMENT]: fetchWorkspaceCatalog — hot path, trả về danh sách workspace tối giản (id, code, name)
// lọc theo zone + context (personal / tenant).
export async function fetchWorkspaceCatalog(
  opts: FetchWorkspaceCatalogOptions,
): Promise<WorkspaceCatalogItem[]> {
  let path = "/api/v1/me/hierarchy/workspace/catalog";

  // [COMMENT]: Tenant context — đường dẫn tenant
  if (opts.tenantContext) {
    path = "/api/v1/tenant/hierarchy/workspaces/catalog";
  }

  // [COMMENT]: Backend trả về { data: WorkspaceCatalogItem[], message: string, ... }
  const res = await fetchJSON<{ data: WorkspaceCatalogItem[] }>(
    path,
    {
      method: "GET",
      signal: opts.signal,
    },
  );

  return res?.data ?? [];
}

export type WorkspaceItem = {
  id: string;
  name: string;
  code: string;
  description: string;
  zone_id: string;
  tenant_id: string | null;
  owner_id: string;
  created_at: string;
};

export type ListWorkspacesOptions = {
  signal?: AbortSignal;
};

// [COMMENT]: listWorkspaces fetches detailed workspaces list from hierarchy api
export async function listWorkspaces(opts?: ListWorkspacesOptions): Promise<WorkspaceItem[]> {
  const res = await fetchJSON<{ data: WorkspaceItem[] }>("/api/v1/me/hierarchy/workspace/read", {
    method: "GET",
    signal: opts?.signal,
  });
  return res?.data ?? [];
}

export type CreateWorkspaceInput = {
  name: string;
  code: string;
  description?: string;
};

// [COMMENT]: createWorkspace posts to hierarchy api to create a new workspace
export async function createWorkspace(input: CreateWorkspaceInput, signal?: AbortSignal): Promise<WorkspaceItem> {
  const res = await fetchJSON<{ data: WorkspaceItem }>("/api/v1/me/hierarchy/workspace/create", {
    method: "POST",
    body: {
      name: input.name,
      code: input.code,
      description: input.description ?? "",
    },
    signal,
  });
  return res?.data;
}

// [COMMENT]: deleteWorkspace sends DELETE request to hierarchy api to remove a workspace
export async function deleteWorkspace(id: string, signal?: AbortSignal): Promise<void> {
  await fetchJSON(`/api/v1/me/hierarchy/workspace/delete/${id}`, {
    method: "DELETE",
    signal,
  });
}
