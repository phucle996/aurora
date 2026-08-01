import RouteGuard from "@/components/route-guard";
import { ManagedServiceInstanceDetailScreen } from "@/features/managed-services/instance-detail-screen";

export default function ManagedServiceInstanceDetailPage() {
  return <RouteGuard requiredKey="managed-service:instance" requiredAction="read"><ManagedServiceInstanceDetailScreen /></RouteGuard>;
}
