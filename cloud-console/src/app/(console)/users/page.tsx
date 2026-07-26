import RouteGuard from "@/components/route-guard";
import { UserDirectoryScreen } from "@/features/iam/users/screen";

export default function UserDirectoryPage() {
  return (
    <RouteGuard requiredKey="iam:users" requiredAction="read">
      <UserDirectoryScreen />
    </RouteGuard>
  );
}
