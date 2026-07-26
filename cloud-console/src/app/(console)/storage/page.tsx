import RouteGuard from "@/components/route-guard";
import { StorageDirectoryScreen } from "@/features/storage/buckets/screen";

export default function StorageDirectoryPage() {
  return (
    <RouteGuard requiredKey="storage:bucket" requiredAction="read">
      <StorageDirectoryScreen />
    </RouteGuard>
  );
}
