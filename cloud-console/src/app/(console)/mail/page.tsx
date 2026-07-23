"use client";

import Link from "next/link";
import { ArrowRight, FileCode2, Mail, Plus, Radio } from "lucide-react";

import RouteGuard from "@/components/route-guard";
import { useWorkspace } from "@/context/WorkspaceContext";
import { useUserSession } from "@/hooks/useUserSession";


function MailOverviewContent() {
  const { activeWorkspaceID, catalog, loading } = useWorkspace();
  const { checkPermission } = useUserSession();
  const workspace = catalog.find((item) => item.id === activeWorkspaceID);

  const canConsumerRead = checkPermission("email:consumer", "read");
  const canTemplateRead = checkPermission("email:template", "read");
  const canTemplateCreate = checkPermission("email:template", "create");



  return (
    <div className="flex min-h-[calc(100vh-110px)] w-full flex-col gap-6 px-6 pb-10 text-foreground">
      {/* Header */}
      <header className="flex flex-col gap-3 border-b border-border pb-5 md:flex-row md:items-end md:justify-between">
        <div className="flex items-start gap-3">
          <div className="flex size-10 items-center justify-center rounded-lg border border-blue-500/20 bg-blue-500/10 text-blue-500">
            <Mail className="size-5" />
          </div>
          <div>
            <h1 className="text-xl font-bold tracking-tight">Email Delivery Overview</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              Monitor operational delivery metrics, Kafka consumers, and immutable email templates.
            </p>
          </div>
        </div>
        <div className="flex items-center gap-3">
          <div className="rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
            Workspace: <span className="font-semibold text-foreground">{loading ? "Loading…" : workspace?.name ?? "Not selected"}</span>
          </div>
          {canTemplateCreate && (
            <Link
              href="/mail/templates/new"
              className="flex items-center gap-1.5 rounded-lg bg-blue-600 px-3.5 py-2 text-xs font-semibold text-white hover:bg-blue-500 transition-all shadow-xs"
            >
              <Plus className="size-4" />
              <span>New Template</span>
            </Link>
          )}
        </div>
      </header>

      {!activeWorkspaceID && !loading ? (
        <div className="rounded-lg border border-dashed p-10 text-center text-sm text-muted-foreground">
          Select or create a workspace before viewing mail overview.
        </div>
      ) : (
        <div className="grid gap-6 md:grid-cols-2">
          {/* Consumer Overview Card */}
          <div className="flex flex-col justify-between rounded-xl border border-border/80 bg-card p-6 shadow-xs transition-all hover:border-blue-500/30">
            <div>
              <div className="flex items-center justify-between pb-4 border-b border-border/50">
                <div className="flex items-center gap-2.5">
                  <div className="flex size-8 items-center justify-center rounded-md bg-blue-500/10 text-blue-500">
                    <Radio className="size-4" />
                  </div>
                  <h2 className="text-base font-semibold">Kafka Consumers</h2>
                </div>
                {canConsumerRead && (
                  <Link
                    href="/mail/consumers"
                    className="flex items-center gap-1 text-xs font-semibold text-blue-500 hover:text-blue-400 transition-colors"
                  >
                    <span>Manage</span>
                    <ArrowRight className="size-3.5" />
                  </Link>
                )}
              </div>

              {canConsumerRead ? (
                // [COMMENT]: Summary API chưa có backend route — hiển thị placeholder cho đến khi Phase 5 được ship.
                <div className="mt-5 rounded-lg border border-dashed border-border/50 p-6 text-center">
                  <p className="text-xs text-muted-foreground">Consumer metrics will be available after the summary backend is deployed.</p>
                </div>
              ) : (
                <p className="mt-4 text-xs text-muted-foreground">You do not have clearance to view consumer metrics (requires email:consumer:read).</p>
              )}
            </div>

            {canConsumerRead && (
              <div className="mt-6 pt-4 border-t border-border/50">
                <Link
                  href="/mail/consumers"
                  className="flex w-full items-center justify-center gap-2 rounded-lg border border-border bg-muted/20 py-2.5 text-xs font-semibold text-foreground hover:bg-muted/50 transition-all"
                >
                  <span>View All Consumers</span>
                  <ArrowRight className="size-3.5" />
                </Link>
              </div>
            )}
          </div>

          {/* Template Overview Card */}
          <div className="flex flex-col justify-between rounded-xl border border-border/80 bg-card p-6 shadow-xs transition-all hover:border-blue-500/30">
            <div>
              <div className="flex items-center justify-between pb-4 border-b border-border/50">
                <div className="flex items-center gap-2.5">
                  <div className="flex size-8 items-center justify-center rounded-md bg-purple-500/10 text-purple-500">
                    <FileCode2 className="size-4" />
                  </div>
                  <h2 className="text-base font-semibold">Email Templates</h2>
                </div>
                {canTemplateRead && (
                  <Link
                    href="/mail/templates"
                    className="flex items-center gap-1 text-xs font-semibold text-purple-500 hover:text-purple-400 transition-colors"
                  >
                    <span>Manage</span>
                    <ArrowRight className="size-3.5" />
                  </Link>
                )}
              </div>

              {canTemplateRead ? (
                // [COMMENT]: Summary API chưa có backend route — hiển thị placeholder cho đến khi Phase 5 được ship.
                <div className="mt-5 rounded-lg border border-dashed border-border/50 p-6 text-center">
                  <p className="text-xs text-muted-foreground">Template metrics will be available after the summary backend is deployed.</p>
                </div>
              ) : (
                <p className="mt-4 text-xs text-muted-foreground">You do not have clearance to view template metrics (requires email:template:read).</p>
              )}
            </div>

            {canTemplateRead && (
              <div className="mt-6 pt-4 border-t border-border/50 flex gap-3">
                <Link
                  href="/mail/templates"
                  className="flex flex-1 items-center justify-center gap-2 rounded-lg border border-border bg-muted/20 py-2.5 text-xs font-semibold text-foreground hover:bg-muted/50 transition-all"
                >
                  <span>View All Templates</span>
                  <ArrowRight className="size-3.5" />
                </Link>
                {canTemplateCreate && (
                  <Link
                    href="/mail/templates/new"
                    className="flex items-center justify-center gap-1.5 rounded-lg bg-purple-600 px-4 py-2.5 text-xs font-semibold text-white hover:bg-purple-500 transition-all shadow-xs"
                  >
                    <Plus className="size-3.5" />
                    <span>New Template</span>
                  </Link>
                )}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

export default function MailConsolePage() {
  return (
    <RouteGuard customCheck={(check) => check("email:consumer", "read") || check("email:template", "read")}>
      <MailOverviewContent />
    </RouteGuard>
  );
}
