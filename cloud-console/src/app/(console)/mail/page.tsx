"use client";

import { useState } from "react";
import { FileCode2, Mail, Radio } from "lucide-react";

import RouteGuard from "@/components/route-guard";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useWorkspace } from "@/context/WorkspaceContext";
import { useUserSession } from "@/hooks/useUserSession";
import { ConsumersTab } from "./components/ConsumersTab";
import { TemplatesTab } from "./components/TemplatesTab";

function MailConsoleContent() {
  const [tab, setTab] = useState("consumers");
  const { activeWorkspaceID, catalog, loading } = useWorkspace();
  const { renderContext, checkPermission } = useUserSession();
  const workspace = catalog.find((item) => item.id === activeWorkspaceID);
  // [COMMENT]: Scope chỉ tham gia query key; HTTP luôn gọi public /api/v1/mail để ACR rewrite fail-close.
  const scopeKey = `${renderContext?.is_personal ? "personal" : "tenant"}:${activeWorkspaceID ?? "none"}`;

  return (
    <div className="flex min-h-[calc(100vh-110px)] w-full flex-col gap-6 px-6 pb-10 text-foreground">
      <header className="flex flex-col gap-3 border-b border-border pb-5 md:flex-row md:items-end md:justify-between">
        <div className="flex items-start gap-3">
          <div className="flex size-10 items-center justify-center rounded-lg border border-blue-500/20 bg-blue-500/10 text-blue-500">
            <Mail className="size-5" />
          </div>
          <div>
            <h1 className="text-xl font-bold tracking-tight">Mail</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              Configure Kafka consumers and immutable email templates for this workspace.
            </p>
          </div>
        </div>
        <div className="rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
          Workspace: <span className="font-semibold text-foreground">{loading ? "Loading…" : workspace?.name ?? "Not selected"}</span>
        </div>
      </header>

      {!activeWorkspaceID && !loading ? (
        <div className="rounded-lg border border-dashed p-10 text-center text-sm text-muted-foreground">
          Select or create a workspace before configuring mail.
        </div>
      ) : (
        <Tabs value={tab} onValueChange={(value) => setTab(value as string)}>
          <TabsList variant="line" className="border-b">
            <TabsTrigger value="consumers"><Radio />Consumers</TabsTrigger>
            <TabsTrigger value="templates"><FileCode2 />Templates</TabsTrigger>
          </TabsList>
          <TabsContent value="consumers" className="pt-4">
            <ConsumersTab
              enabled={Boolean(activeWorkspaceID)}
              scopeKey={scopeKey}
              canCreate={checkPermission("mail:mail", "create")}
              canUpdate={checkPermission("mail:mail", "update")}
              canDelete={checkPermission("mail:mail", "delete")}
            />
          </TabsContent>
          <TabsContent value="templates" className="pt-4">
            <TemplatesTab
              enabled={Boolean(activeWorkspaceID)}
              scopeKey={scopeKey}
              canCreate={checkPermission("mail:mail", "create")}
              canUpdate={checkPermission("mail:mail", "update")}
              canDelete={checkPermission("mail:mail", "delete")}
            />
          </TabsContent>
        </Tabs>
      )}
    </div>
  );
}

export default function MailConsolePage() {
  return (
    <RouteGuard requiredKey="mail:mail" requiredAction="read">
      <MailConsoleContent />
    </RouteGuard>
  );
}
