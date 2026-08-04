import type { ReactNode } from "react";

import { WorkspaceInitializer } from "@/components/workspace-initializer";
import { WorkspaceProvider } from "@/context/WorkspaceContext";
import { ConsoleShell } from "@/shell/console-shell";

export default function TenantConsoleLayout({ children }: { children: ReactNode }) {
  return (
    <WorkspaceProvider>
      <WorkspaceInitializer kind="tenant" />
      <ConsoleShell kind="tenant">{children}</ConsoleShell>
    </WorkspaceProvider>
  );
}
