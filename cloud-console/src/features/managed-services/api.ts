import { fetchJSON } from "@/shared/api/http";
import {
  decodeManagedServiceCatalogItem,
  decodeManagedServiceVersionContract,
  localizedText,
} from "@/features/managed-services/contract";
import type { ManagedServiceCatalogPage, ManagedServiceVersionContract } from "@/features/managed-services/model";

export async function listManagedServiceCatalog(personal: boolean, cursor: string, signal?: AbortSignal): Promise<ManagedServiceCatalogPage> {
  const branch = personal ? "personal" : "tenant";
  const query = new URLSearchParams({ limit: "100" });
  if (cursor) query.set("cursor", cursor);
  const response = await fetchJSON<{ data?: { items?: unknown[]; next_cursor?: unknown } }>(`/api/v1/${branch}/managed-services/catalog?${query.toString()}`, {
    method: "GET",
    signal,
    cache: "no-store",
  });
  if (!Array.isArray(response.data?.items) || typeof response.data.next_cursor !== "string" || response.data.next_cursor.length > 128) {
    throw new Error("Managed Service catalog response is invalid.");
  }
  return { items: response.data.items.map(decodeManagedServiceCatalogItem), next_cursor: response.data.next_cursor };
}

export async function getManagedServiceVersionContract(
  personal: boolean,
  versionID: string,
  expectedRevisionID: string,
  signal?: AbortSignal,
): Promise<ManagedServiceVersionContract> {
  const branch = personal ? "personal" : "tenant";
  const response = await fetchJSON<{ data?: unknown }>(
    `/api/v1/${branch}/managed-services/catalog/versions/${encodeURIComponent(versionID)}?expected_revision_id=${encodeURIComponent(expectedRevisionID)}`,
    { method: "GET", signal, cache: "no-store" },
  );
  return decodeManagedServiceVersionContract(response.data);
}

export { localizedText };
