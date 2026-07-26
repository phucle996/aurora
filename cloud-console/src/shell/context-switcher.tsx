"use client";

import { useWorkspace } from "@/context/WorkspaceContext";
import { WorkspaceSelector } from "@/shell/workspace-selector";
import { ZoneSelector } from "@/shell/zone-selector";

export function ContextSwitcher({ mobile = false }: { mobile?: boolean }) {
  const { catalog, activeWorkspaceID, selectWorkspace, loading } = useWorkspace();

  return (
    <div
      className={
        mobile
          ? "grid gap-1 border-b border-sidebar-console-border px-2 py-3"
          : "flex items-center gap-2"
      }
      aria-label="Console context"
    >
      <WorkspaceSelector
        catalog={catalog}
        activeWorkspaceID={activeWorkspaceID}
        selectWorkspace={selectWorkspace}
        loading={loading}
        compact={mobile}
      />
      {!mobile && <div className="h-4 w-px bg-border" />}
      <ZoneSelector compact={mobile} />
    </div>
  );
}
