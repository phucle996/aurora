import type { LucideIcon } from "lucide-react";
import { publicRuntimeConfig } from "@/runtime-config";
import {
  Boxes,
  Coins,
  HardDrive,
  LayoutDashboard,
  LayoutGrid,
  Lock,
  Mail,
  Server,
  Users,
  ArrowLeftRight,
} from "lucide-react";

export type NavigationId =
  | "overview"
  | "workspaces"
  | "users"
  | "rbac"
  | "storage"
  | "compute"
  | "mail"
  | "managed-services"
  | "billing"
  | "context";

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

export type ConsoleKind = "personal" | "tenant";

type PermissionCheck = (key: string, action: string) => boolean;

export function personalConsoleNavigation(checkPermission: PermissionCheck): NavigationItem[] {
  const items: NavigationItem[] = [
    {
      id: "overview",
      label: "Overview",
      path: "/personal",
      icon: LayoutDashboard,
      breadcrumb: ["Console", "Overview"],
    },
    {
      id: "workspaces",
      label: "My Workspaces",
      path: "/personal/workspaces",
      icon: LayoutGrid,
      breadcrumb: ["Console", "Workspaces"],
      permission: {
        key: "hierarchy:workspace",
        action: "read",
      },
    },
    {
      id: "users",
      label: "User Directory",
      path: "/personal/users",
      icon: Users,
      breadcrumb: ["Console", "User Directory"],
      permission: { key: "iam:users", action: "read" },
    },
    {
      id: "rbac",
      label: "Access Control",
      path: "/personal/rbac",
      icon: Lock,
      breadcrumb: ["Console", "Access Control"],
      permission: { key: "iam:role", action: "read" },
    },
    {
      id: "compute",
      label: "Virtual Machines",
      path: "/personal/compute",
      icon: Server,
      breadcrumb: ["Console", "Virtual Machines"],
      permission: { key: "hypervisor:vm", action: "read" },
    },
    {
      id: "storage",
      label: "Object Storage",
      path: "/personal/storage",
      icon: HardDrive,
      breadcrumb: ["Console", "Object Storage"],
      permission: { key: "storage:bucket", action: "read" },
    },
    {
      id: "mail",
      label: "Email Delivery",
      path: "/personal/mail",
      icon: Mail,
      breadcrumb: ["Console", "Email Delivery"],
      anyPermission: [
        { key: "email:consumer", action: "read" },
        { key: "email:template", action: "read" },
      ],
    },
    {
      id: "managed-services",
      label: "Managed Services",
      path: "/personal/managed-services",
      icon: Boxes,
      breadcrumb: ["Console", "Managed Services"],
      permission: { key: "managed-service:instance", action: "read" },
    },
    {
      id: "billing",
      label: "Cost Management",
      icon: Coins,
      breadcrumb: ["Console", "Cost Management"],
      external: "billing",
    },
    { id: "context", label: "Window Context", path: "/personal/context", icon: ArrowLeftRight, breadcrumb: ["Platform", "Window Context"] },
  ];

  return items.filter((item) => {
    if (item.anyPermission) {
      return item.anyPermission.some(({ key, action }) => checkPermission(key, action));
    }
    return !item.permission || checkPermission(item.permission.key, item.permission.action);
  });
}

export function tenantConsoleNavigation(checkPermission: PermissionCheck): NavigationItem[] {
  const items: NavigationItem[] = [
    {
      id: "overview",
      label: "Tenant Overview",
      path: "/tenant",
      icon: LayoutDashboard,
      breadcrumb: ["Tenant Console", "Overview"],
    },
    {
      id: "workspaces",
      label: "Workspaces",
      path: "/tenant/workspaces",
      icon: LayoutGrid,
      breadcrumb: ["Tenant Console", "Workspaces"],
      permission: { key: "tenant:workspaces", action: "list" },
    },
    {
      id: "rbac",
      label: "Access Control",
      path: "/tenant/rbac",
      icon: Lock,
      breadcrumb: ["Tenant Console", "Access Control"],
      permission: { key: "iam:role", action: "read" },
    },
    {
      id: "compute",
      label: "Virtual Machines",
      path: "/tenant/compute",
      icon: Server,
      breadcrumb: ["Tenant Console", "Virtual Machines"],
      permission: { key: "hypervisor:vm", action: "read" },
    },
    {
      id: "storage",
      label: "Object Storage",
      path: "/tenant/storage",
      icon: HardDrive,
      breadcrumb: ["Tenant Console", "Object Storage"],
      permission: { key: "storage:bucket", action: "read" },
    },
    {
      id: "mail",
      label: "Email Delivery",
      path: "/tenant/mail",
      icon: Mail,
      breadcrumb: ["Tenant Console", "Email Delivery"],
      anyPermission: [
        { key: "email:consumer", action: "read" },
        { key: "email:template", action: "read" },
      ],
    },
    {
      id: "managed-services",
      label: "Managed Services",
      path: "/tenant/managed-services",
      icon: Boxes,
      breadcrumb: ["Tenant Console", "Managed Services"],
      permission: { key: "managed-service:instance", action: "read" },
    },
    {
      id: "billing",
      label: "Cost Management",
      icon: Coins,
      breadcrumb: ["Tenant Console", "Cost Management"],
      external: "billing",
    },
    { id: "context", label: "Window Context", path: "/tenant/context", icon: ArrowLeftRight, breadcrumb: ["Platform", "Window Context"] },
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
  const configured = publicRuntimeConfig()?.costConsoleUrl.trim();
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
