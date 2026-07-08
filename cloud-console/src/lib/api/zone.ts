import { fetchJSON } from "./fetcher";

// [COMMENT]: Định nghĩa cấu trúc dữ liệu của một Zone trả về từ Catalog API tại Edge Envoy/ACL.
export type ZoneCatalogItem = {
  id: string;
  code: string;
  name: string;
  status: string;
};

// [COMMENT]: Cache zone catalog trong RAM & cache promise request đang chạy
// để tránh duplicate parallel HTTP requests khi nhiều component mount cùng lúc.
let cachedZones: ZoneCatalogItem[] | null = null;
let activeZoneCatalogPromise: Promise<ZoneCatalogItem[]> | null = null;

export async function fetchZoneCatalog(options: { signal?: AbortSignal } = {}): Promise<ZoneCatalogItem[]> {
  if (cachedZones) return cachedZones;
  if (activeZoneCatalogPromise) return activeZoneCatalogPromise;

  activeZoneCatalogPromise = fetchJSON<ZoneCatalogItem[]>("/api/v1/zones/catalog", {
    method: "GET",
    signal: options.signal,
  }).then((data) => {
    cachedZones = data;
    return data;
  });

  activeZoneCatalogPromise.finally(() => {
    activeZoneCatalogPromise = null;
  });

  return activeZoneCatalogPromise;
}

// [COMMENT]: Hàm gọi API chuyển đổi Active Zone tường minh qua Edge Ingress/ACL.
// Trả về JSON Body chứa zone_code và zone_id mới để UI cập nhật state đồng bộ.
export async function switchZone(
  zoneCode: string,
  options: { signal?: AbortSignal } = {},
): Promise<{ zone_code: string }> {
  return fetchJSON<{ zone_code: string }>(
    `/api/v1/zone/go-to-zone?zone_code=${zoneCode}`,
    {
      method: "POST",
      credentials: "include",
      signal: options.signal,
    }
  );
}
