import RouteGuard from "@/components/route-guard";
import { ComputeScreen } from "@/features/compute/screen";

export default function ComputePage() {
  return (
    <RouteGuard requiredKey="hypervisor:vm" requiredAction="read">
      <ComputeScreen />
    </RouteGuard>
  );
}
