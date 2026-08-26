import { fetchJSON } from "@/shared/api/http";
import { criticalFetchJSON } from "@/shared/api/critical";

// [COMMENT]: WorkspaceCatalogItem — dữ liệu tối giản từ hot path catalog, khớp với entity Go (id, code, name)
export type WorkspaceCatalogItem = {
  id: string;
  code: string;
  name: string;
};

// [COMMENT]: fetchWorkspaceCatalog — hot path, trả về danh sách workspace tối giản (id, code, name)
// lọc theo zone + context (personal / tenant).
export async function fetchWorkspaceCatalog(
  signal?: AbortSignal,
): Promise<WorkspaceCatalogItem[]> {
  // [COMMENT]: Backend trả về { data: WorkspaceCatalogItem[], message: string, ... }
  const res = await fetchJSON<{ data: WorkspaceCatalogItem[] }>(
    "/api/v1/hierarchy/workspaces/catalog",
    {
      method: "GET",
      signal,
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
  const res = await fetchJSON<{ data: WorkspaceItem[] }>("/api/v1/hierarchy/workspaces", {
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
  const res = await criticalFetchJSON<{ data: WorkspaceItem }>("/api/v1/critical/hierarchy/workspaces", {
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

// [COMMENT]: The API deletes only the active workspace selected by the verified
// ACR context. The browser never sends a workspace ID for deletion.
export async function deleteWorkspace(signal?: AbortSignal): Promise<void> {
  await criticalFetchJSON("/api/v1/critical/hierarchy/workspaces", {
    method: "DELETE",
    signal,
  });
}
