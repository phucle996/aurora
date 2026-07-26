import type { ComponentType } from "react";
import {
  LayoutDashboard,
  Coins,
  Receipt,
  CreditCard,
  History,
  TicketPercent,
} from "lucide-react";

export interface NavigationItem {
  id: string;
  name: string;
  path: string;
  icon: ComponentType<{ size?: number; className?: string }>;
  permission?: { key: string; action: string };
  anyPermission?: Array<{ key: string; action: string }>;
}

// This is presentation metadata only. IAM Render Context decides which entries
// survive; Cost Manager Authorize remains the execution boundary.
export const navigationItems: NavigationItem[] = [
  { id: "dashboard", name: "Dashboard", path: "/", icon: LayoutDashboard },
  {
    id: "plans",
    name: "Gói Cước & Giá",
    path: "/plan",
    icon: Coins,
    anyPermission: [
      { key: "billing:plan", action: "read" },
      { key: "billing:tier", action: "read" },
    ],
  },
  {
    id: "invoices",
    name: "Hóa Đơn Kế Toán",
    path: "/invoices",
    icon: Receipt,
    permission: { key: "billing:ledger", action: "read" },
  },
  {
    id: "gateways",
    name: "Cổng Nạp Tiền",
    path: "/gateways",
    icon: CreditCard,
    permission: { key: "billing:wallet", action: "read" },
  },
  {
    id: "history",
    name: "Lịch Sử Giao Dịch",
    path: "/history",
    icon: History,
    permission: { key: "billing:ledger", action: "read" },
  },
  {
    id: "referrals",
    name: "Referral",
    path: "/referrals",
    icon: TicketPercent,
    permission: { key: "billing:credit", action: "adjust" },
  },
];
