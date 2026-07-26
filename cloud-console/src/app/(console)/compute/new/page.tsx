import RouteGuard from "@/components/route-guard";
import { CreateComputeScreen } from "@/features/compute/create-screen";

export default function CreateComputePage() {
  return (
    <RouteGuard requiredKey="hypervisor:vm" requiredAction="create">
      <CreateComputeScreen />
    </RouteGuard>
  );
}
