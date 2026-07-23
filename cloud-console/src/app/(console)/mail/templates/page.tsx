"use client";

import Link from "next/link";
import { ArrowLeft, FileCode2, Plus } from "lucide-react";

import RouteGuard from "@/components/route-guard";
import { useWorkspace } from "@/context/WorkspaceContext";
import { useUserSession } from "@/hooks/useUserSession";
import { TemplatesTab } from "../components/TemplatesTab";

function TemplatesContent() {
  const { activeWorkspaceID, catalog, loading } = useWorkspace();
  const { renderContext, checkPermission } = useUserSession();
  const workspace = catalog.find((item) => item.id === activeWorkspaceID);
  const scopeKey = `${renderContext?.is_personal ? "personal" : "tenant"}:${activeWorkspaceID ?? "none"}`;

  const canCreate = checkPermission("email:template", "create");

  return (
    <div className="flex min-h-[calc(100vh-110px)] w-full flex-col gap-6 px-6 pb-10 text-foreground">
      <header className="flex flex-col gap-3 border-b border-border pb-5 md:flex-row md:items-end md:justify-between">
        <div className="flex items-start gap-3">
          <Link
            href="/mail"
            className="flex size-10 items-center justify-center rounded-lg border border-border bg-muted/20 text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-all mt-0.5"
          >
            <ArrowLeft className="size-5" />
          </Link>
          <div>
            <div className="flex items-center gap-2">
              <FileCode2 className="size-4 text-purple-500" />
              <h1 className="text-xl font-bold tracking-tight">Email Templates</h1>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              Manage immutable email templates and version history for this workspace.
            </p>
          </div>
        </div>
        <div className="flex items-center gap-3">
          <div className="rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
            Workspace: <span className="font-semibold text-foreground">{loading ? "Loading…" : workspace?.name ?? "Not selected"}</span>
          </div>
          {canCreate && (
            <Link
              href="/mail/templates/new"
              className="flex items-center gap-1.5 rounded-lg bg-purple-600 px-3.5 py-2 text-xs font-semibold text-white hover:bg-purple-500 transition-all shadow-xs"
            >
              <Plus className="size-4" />
              <span>New Template</span>
            </Link>
          )}
        </div>
      </header>

      {!activeWorkspaceID && !loading ? (
        <div className="rounded-lg border border-dashed p-10 text-center text-sm text-muted-foreground">
          Select or create a workspace before managing email templates.
        </div>
      ) : (
        <TemplatesTab
          enabled={Boolean(activeWorkspaceID)}
          scopeKey={scopeKey}
          canCreate={canCreate}
          canUpdate={checkPermission("email:template", "publish")}
          canDelete={checkPermission("email:template", "delete")}
        />
      )}
    </div>
  );
}

export default function MailTemplatesPage() {
  return (
    <RouteGuard requiredKey="email:template" requiredAction="read">
      <TemplatesContent />
    </RouteGuard>
  );
}
