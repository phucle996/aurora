import type { LucideIcon } from "lucide-react";
import {
  Coins,
  HardDrive,
  LayoutDashboard,
  LayoutGrid,
  Lock,
  Mail,
  Users,
} from "lucide-react";

export type NavigationId =
  | "overview"
  | "workspaces"
  | "users"
  | "rbac"
  | "storage"
  | "mail"
  | "billing";

export type NavigationItem = {
  id: NavigationId;
  label: string;
  path?: string;
  icon: LucideIcon;
  breadcrumb: readonly [string, string];
  external?: "billing";
  permission?: { key: string; action: string };
  anyPermission?: ReadonlyArray<{ key: string; action: string }>;
};

type PermissionCheck = (key: string, action: string) => boolean;

export function consoleNavigation(
  isPersonal: boolean,
  checkPermission: PermissionCheck,
): NavigationItem[] {
  const items: NavigationItem[] = [
    {
      id: "overview",
      label: "Overview",
      path: "/",
      icon: LayoutDashboard,
      breadcrumb: ["Console", "Overview"],
    },
    {
      id: "workspaces",
      label: isPersonal ? "My Workspaces" : "Workspaces",
      path: "/workspaces",
      icon: LayoutGrid,
      breadcrumb: ["Console", "Workspaces"],
      permission: {
        key: isPersonal ? "hierarchy:workspace" : "tenant:workspaces",
        action: isPersonal ? "read" : "list",
      },
    },
    {
      id: "users",
      label: "User Directory",
      path: "/users",
      icon: Users,
      breadcrumb: ["Console", "User Directory"],
      permission: { key: "iam:users", action: "read" },
    },
    {
      id: "rbac",
      label: "Access Control",
      path: "/rbac",
      icon: Lock,
      breadcrumb: ["Console", "Access Control"],
      permission: { key: "iam:role", action: "read" },
    },
    {
      id: "storage",
      label: "Object Storage",
      path: "/storage",
      icon: HardDrive,
      breadcrumb: ["Console", "Object Storage"],
      permission: { key: "storage:bucket", action: "read" },
    },
    {
      id: "mail",
      label: "Email Delivery",
      path: "/mail",
      icon: Mail,
      breadcrumb: ["Console", "Email Delivery"],
      anyPermission: [
        { key: "email:consumer", action: "read" },
        { key: "email:template", action: "read" },
      ],
    },
    {
      id: "billing",
      label: "Cost Management",
      icon: Coins,
      breadcrumb: ["Console", "Cost Management"],
      external: "billing",
    },
  ];

  return items.filter((item) => {
    if (item.anyPermission) {
      return item.anyPermission.some(({ key, action }) => checkPermission(key, action));
    }
    return !item.permission || checkPermission(item.permission.key, item.permission.action);
  });
}

export function activeNavigation(pathname: string, items: NavigationItem[]): NavigationItem {
  return (
    items
      .filter((item) => item.path)
      .sort((left, right) => (right.path?.length ?? 0) - (left.path?.length ?? 0))
      .find((item) =>
        item.path === "/" ? pathname === "/" : pathname === item.path || pathname.startsWith(`${item.path}/`),
      ) ?? items[0]
  );
}

export function billingStartURL(): string | null {
  const configured = process.env.NEXT_PUBLIC_COST_CONSOLE_URL?.trim();
  if (!configured) return null;

  try {
    const origin = new URL(configured);
    const localDevelopment = origin.hostname === "localhost" || origin.hostname === "127.0.0.1";
    if (origin.protocol !== "https:" && !(localDevelopment && origin.protocol === "http:")) {
      return null;
    }
    origin.pathname = "/auth/start";
    origin.search = "";
    origin.hash = "";
    return origin.toString();
  } catch {
    return null;
  }
}
