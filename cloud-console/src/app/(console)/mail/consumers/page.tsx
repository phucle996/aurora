"use client";

import Link from "next/link";
import { ArrowLeft, Plus, Radio } from "lucide-react";

import RouteGuard from "@/components/route-guard";
import { useWorkspace } from "@/context/WorkspaceContext";
import { useUserSession } from "@/hooks/useUserSession";
import { ConsumersTab } from "../components/ConsumersTab";

function ConsumersContent() {
  const { activeWorkspaceID, catalog, loading } = useWorkspace();
  const { renderContext, checkPermission } = useUserSession();
  const workspace = catalog.find((item) => item.id === activeWorkspaceID);
  const scopeKey = `${renderContext?.is_personal ? "personal" : "tenant"}:${activeWorkspaceID ?? "none"}`;

  const canCreate = checkPermission("email:consumer", "create");

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
              <Radio className="size-4 text-blue-500" />
              <h1 className="text-xl font-bold tracking-tight">Kafka Consumers</h1>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              Manage Kafka consumers processing email delivery streams for this workspace.
            </p>
          </div>
        </div>
        <div className="flex items-center gap-3">
          <div className="rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
            Workspace: <span className="font-semibold text-foreground">{loading ? "Loading…" : workspace?.name ?? "Not selected"}</span>
          </div>
          {canCreate && (
            <Link
              href="/mail/consumers/new"
              className="flex items-center gap-1.5 rounded-lg bg-blue-600 px-3.5 py-2 text-xs font-semibold text-white hover:bg-blue-500 transition-all shadow-xs"
            >
              <Plus className="size-4" />
              <span>New Consumer</span>
            </Link>
          )}
        </div>
      </header>

      {!activeWorkspaceID && !loading ? (
        <div className="rounded-lg border border-dashed p-10 text-center text-sm text-muted-foreground">
          Select or create a workspace before managing mail consumers.
        </div>
      ) : (
        <ConsumersTab
          enabled={Boolean(activeWorkspaceID)}
          scopeKey={scopeKey}
          canCreate={canCreate}
          canUpdate={checkPermission("email:consumer", "update")}
          canDelete={checkPermission("email:consumer", "delete")}
        />
      )}
    </div>
  );
}

export default function MailConsumersPage() {
  return (
    <RouteGuard requiredKey="email:consumer" requiredAction="read">
      <ConsumersContent />
    </RouteGuard>
  );
}
