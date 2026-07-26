import RouteGuard from "@/components/route-guard";
import { AccessControlScreen } from "@/features/rbac/screen";

export default function AccessControlPage() {
  return (
    <RouteGuard requiredKey="iam:role" requiredAction="read">
      <AccessControlScreen />
    </RouteGuard>
  );
}
