"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { KeyRound, Laptop, Link2, UserRound } from "lucide-react";

import { cn } from "@/lib/utils";

const settingsTabs = [
  { href: "/settings/personalization", label: "Personalization", icon: UserRound },
  { href: "/settings/mfa", label: "MFA", icon: KeyRound },
  { href: "/settings/social-links", label: "Social links", icon: Link2 },
  { href: "/settings/devices", label: "Devices", icon: Laptop },
] as const;

export function SettingsNav() {
  const pathname = usePathname();

  return (
    <nav className="-mx-1 flex gap-1 overflow-x-auto px-1 pb-1" aria-label="Settings sections">
      {settingsTabs.map((tab) => {
        const active = pathname === tab.href;
        const Icon = tab.icon;
        return (
          <Link
            key={tab.href}
            href={tab.href}
            aria-current={active ? "page" : undefined}
            className={cn(
              "inline-flex h-9 shrink-0 items-center gap-2 rounded-[6px] px-3 text-xs font-semibold outline-none transition-colors focus-visible:ring-2 focus-visible:ring-blue-500",
              active
                ? "bg-blue-600 text-white shadow-sm"
                : "text-muted-foreground hover:bg-muted hover:text-foreground",
            )}
          >
            <Icon className="h-3.5 w-3.5" />
            {tab.label}
          </Link>
        );
      })}
    </nav>
  );
}
