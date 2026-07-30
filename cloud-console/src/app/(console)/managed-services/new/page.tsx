import RouteGuard from "@/components/route-guard";
import { CreateManagedServiceScreen } from "@/features/managed-services/create-screen";

export default function CreateManagedServicePage() {
  return <RouteGuard requiredKey="managed-service:catalog" requiredAction="read"><CreateManagedServiceScreen /></RouteGuard>;
}
