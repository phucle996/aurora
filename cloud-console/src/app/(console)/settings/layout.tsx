import type { ReactNode } from "react";
import { Settings } from "lucide-react";

import { SettingsNav } from "@/features/settings/settings-nav";

export default function SettingsLayout({ children }: { children: ReactNode }) {
  return (
    <div className="mx-auto w-full max-w-5xl pb-10">
      <header className="flex items-start gap-3 border-b border-border pb-5">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-[6px] border border-blue-500/20 bg-blue-600/10 text-blue-500">
          <Settings className="h-5 w-5" />
        </div>
        <div>
          <h1 className="text-xl font-bold tracking-tight">Settings</h1>
          <p className="mt-1 max-w-2xl text-xs font-medium leading-relaxed text-muted-foreground">
            Manage your profile, sign-in protection, linked identities and active devices.
          </p>
        </div>
      </header>
      <div className="mt-4 border-b border-border pb-3">
        <SettingsNav />
      </div>
      <div className="mt-6">{children}</div>
    </div>
  );
}
