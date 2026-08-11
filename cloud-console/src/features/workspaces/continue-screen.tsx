"use client";

import { useEffect, useRef } from "react";
import { useRouter } from "next/navigation";
import { ArrowRight, LayoutGrid, Loader2 } from "lucide-react";

import { useWorkspace } from "@/context/WorkspaceContext";

export function WorkspaceContinueScreen({ kind }: { kind: "personal" | "tenant" }) {
  const router = useRouter();
  const userSelectedWorkspace = useRef(false);
  const {
    catalog,
    activeWorkspaceID,
    clearActiveWorkspaceSelection,
    loading,
    selectWorkspace,
  } = useWorkspace();

  useEffect(() => {
    if (!userSelectedWorkspace.current && activeWorkspaceID) {
      clearActiveWorkspaceSelection();
    }
  }, [activeWorkspaceID, clearActiveWorkspaceSelection]);

  const selectAndContinue = (workspaceID: string) => {
    userSelectedWorkspace.current = true;
    selectWorkspace(workspaceID);
    router.replace(kind === "tenant" ? "/tenant/workspaces" : "/personal/workspaces");
  };

  return (
    <section className="mx-auto flex max-w-2xl flex-col items-center py-12 text-center sm:py-20">
      <div className="flex h-12 w-12 items-center justify-center rounded-xl border border-blue-500/20 bg-blue-500/10 text-blue-500">
        <LayoutGrid className="h-5 w-5" />
      </div>
      <h1 className="mt-5 text-xl font-bold tracking-tight text-foreground">Choose where to continue</h1>
      <p className="mt-2 max-w-lg text-sm leading-6 text-muted-foreground">
        Your workspace was deleted. Select another workspace when you are ready to continue.
      </p>

      {loading ? (
        <div className="mt-8 flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading workspaces...
        </div>
      ) : catalog.length > 0 ? (
        <div className="mt-8 grid w-full gap-3 text-left sm:grid-cols-2">
          {catalog.map((workspace) => (
            <button
              key={workspace.id}
              type="button"
              onClick={() => selectAndContinue(workspace.id)}
              className="group rounded-xl border border-border bg-card p-4 text-left transition-colors hover:border-blue-500/40 hover:bg-blue-500/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
            >
              <div className="flex items-start justify-between gap-3">
                <div>
                  <p className="font-semibold text-foreground">{workspace.name}</p>
                  <p className="mt-1 text-xs font-mono text-muted-foreground">{workspace.code}</p>
                </div>
                <ArrowRight className="mt-1 h-4 w-4 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-blue-500" />
              </div>
            </button>
          ))}
        </div>
      ) : (
        <p className="mt-8 text-sm text-muted-foreground">No workspace is available in this context.</p>
      )}
    </section>
  );
}
