"use client";

// [COMMENT]: Khai báo "use client" để cho phép truyền hàm customCheck callback dưới dạng Client Component sang RouteGuard
import RouteGuard from "@/components/route-guard";
import { CreateManagedServiceScreen } from "@/features/managed-services/create-screen";

// [COMMENT]: Trang tạo Managed Service kiểm tra phân quyền truy cập Catalog read & Instance write trước khi render màn hình khởi tạo
export default function CreateManagedServicePage() {
  return (
    <RouteGuard customCheck={(checkPermission) => checkPermission("managed-service:catalog", "read") && checkPermission("managed-service:instance", "write")}>
      <CreateManagedServiceScreen />
    </RouteGuard>
  );
}

