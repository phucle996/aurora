import RouteGuard from "@/components/route-guard";
import { ManagedServiceInstancesScreen } from "@/features/managed-services/instances-screen";

export default function ManagedServicesPage() {
  return <RouteGuard requiredKey="managed-service:instance" requiredAction="read"><ManagedServiceInstancesScreen /></RouteGuard>;
}
