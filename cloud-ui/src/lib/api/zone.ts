import { fetchJSON } from "./fetcher";

// [COMMENT]: Định nghĩa cấu trúc dữ liệu của một Zone trả về từ Catalog API tại Edge Envoy/ACL.
export type ZoneCatalogItem = {
  id: string;
  code: string;
  name: string;
  status: string;
};

// [COMMENT]: Hàm fetch danh mục Zone phục vụ hiển thị ở form đăng nhập khi chưa authenticated.
// Do API này được đánh chặn tại Envoy Edge nên latency cực thấp và có sẵn cơ chế bảo vệ DDoS.
export async function fetchZoneCatalog(options: { signal?: AbortSignal } = {}): Promise<ZoneCatalogItem[]> {
  return fetchJSON<ZoneCatalogItem[]>("/api/v1/zones/catalog", {
    method: "GET",
    signal: options.signal,
  });
}
