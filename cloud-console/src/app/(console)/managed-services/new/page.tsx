import RouteGuard from "@/components/route-guard";
import { CreateManagedServiceScreen } from "@/features/managed-services/create-screen";

export default function CreateManagedServicePage() {
  return <RouteGuard customCheck={(checkPermission) => checkPermission("managed-service:catalog", "read") && checkPermission("managed-service:instance", "write")}><CreateManagedServiceScreen /></RouteGuard>;
}
