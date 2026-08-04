import type { ReactNode } from "react";

import { WorkspaceInitializer } from "@/components/workspace-initializer";
import { WorkspaceProvider } from "@/context/WorkspaceContext";
import { ConsoleShell } from "@/shell/console-shell";

export default function PersonalConsoleLayout({ children }: { children: ReactNode }) {
  return (
    <WorkspaceProvider>
      <WorkspaceInitializer kind="personal" />
      <ConsoleShell kind="personal">{children}</ConsoleShell>
    </WorkspaceProvider>
  );
}
