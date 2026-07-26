import type { ReactNode } from "react";
import { ShieldAlert } from "lucide-react";

import { useAuthStore } from "../lib/store/useAuthStore";

type RouteGuardProps = {
  children: ReactNode;
  requiredKey?: string;
  requiredAction?: string;
  customCheck?: (checkPermission: (key: string, action: string) => boolean) => boolean;
};

export function RouteGuard({
  children,
  requiredKey,
  requiredAction,
  customCheck,
}: RouteGuardProps) {
  const { isAuthenticated, isLoading, checkPermission } = useAuthStore();
  if (isLoading) {
    return (
      <div className="flex min-h-64 items-center justify-center text-xs font-semibold text-slate-500">
        Verifying billing permissions…
      </div>
    );
  }

  const authorized = isAuthenticated && (
    customCheck
      ? customCheck(checkPermission)
      : Boolean(requiredKey && requiredAction && checkPermission(requiredKey, requiredAction))
  );
  if (!authorized) {
    return (
      <div className="flex min-h-64 flex-col items-center justify-center rounded-xl border border-rose-900/40 bg-rose-950/10 px-6 text-center">
        <ShieldAlert className="mb-3 h-8 w-8 text-rose-500" />
        <h2 className="text-sm font-bold text-slate-100">Access denied</h2>
        <p className="mt-1 max-w-md text-xs text-slate-500">
          IAM did not grant the permission required to render this Cost Console feature.
        </p>
      </div>
    );
  }

  return children;
}
