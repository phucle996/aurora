"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { KeyRound, Laptop, Link2, UserRound } from "lucide-react";

import { cn } from "@/lib/utils";

const settingsTabs = [
  { suffix: "/personalization", label: "Personalization", icon: UserRound },
  { suffix: "/mfa", label: "MFA", icon: KeyRound },
  { suffix: "/social-links", label: "Social links", icon: Link2 },
  { suffix: "/devices", label: "Devices", icon: Laptop },
] as const;

export function SettingsNav({ basePath = "/settings" }: { basePath?: string }) {
  const pathname = usePathname();

  return (
    <nav className="-mx-1 flex gap-1 overflow-x-auto px-1 pb-1" aria-label="Settings sections">
      {settingsTabs.map((tab) => {
        const href = `${basePath}${tab.suffix}`;
        const active = pathname === href;
        const Icon = tab.icon;
        return (
          <Link
            key={href}
            href={href}
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
