import RouteGuard from "@/components/route-guard";
import { ManagedServicesCatalogScreen } from "@/features/managed-services/catalog-screen";

export default function ManagedServiceCatalogPage() {
  return <RouteGuard requiredKey="managed-service:catalog" requiredAction="read"><ManagedServicesCatalogScreen /></RouteGuard>;
}
