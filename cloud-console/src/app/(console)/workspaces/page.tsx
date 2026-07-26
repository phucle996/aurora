import RouteGuard from "@/components/route-guard";
import { WorkspacesScreen } from "@/features/workspaces/screen";

export default function WorkspacesPage() {
  return (
    <RouteGuard requiredKey="hierarchy:workspace" requiredAction="read">
      <WorkspacesScreen />
    </RouteGuard>
  );
}
