"use client";

import Link from "next/link";
import { useMemo } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Boxes, ExternalLink, Plus, RefreshCw } from "lucide-react";

import { Button, buttonVariants } from "@/components/ui/button";
import { listManagedServiceInstances } from "@/features/managed-services/api";
import { useManagedServiceRealtime } from "@/features/managed-services/realtime";
import { useConsoleQueryScope } from "@/shared/query/scope";
import { useUserSession } from "@/session/use-session";
import { useWorkspace } from "@/context/WorkspaceContext";
import { isAPIError } from "@/shared/api/http";
import type { ManagedServiceInstanceSummary } from "@/features/managed-services/model";

function stateLabel(value: string): string {
  return value.replaceAll("_", " ").toLowerCase().replace(/(^|\s)\S/g, (letter) => letter.toUpperCase());
}

function stateTone(value: string): string {
  if (value === "active" || value === "succeeded") return "bg-emerald-500";
  if (value === "failed" || value === "terminal_failed") return "bg-red-500";
  return "bg-amber-500";
}

function InstanceRow({ item }: { item: ManagedServiceInstanceSummary }) {
  const operation = item.latest_operation;
  return (
    <tr className="border-b border-border last:border-0 hover:bg-muted/30">
      <td className="px-4 py-3">
        <Link className="group inline-flex items-center gap-1.5" href={`/managed-services/${encodeURIComponent(item.code)}`}>
          <span className="font-semibold group-hover:underline">{item.name}</span>
          <ExternalLink className="h-3 w-3 text-muted-foreground" />
        </Link>
        <div className="mt-0.5 font-mono text-[10px] text-muted-foreground">{item.code}</div>
      </td>
      <td className="px-4 py-3">
        <span className="inline-flex items-center gap-2 text-xs font-semibold">
          <span className={`h-2 w-2 rounded-full ${stateTone(item.desired.state)}`} />
          {stateLabel(item.desired.state)}
        </span>
      </td>
      <td className="px-4 py-3 text-xs text-muted-foreground">{stateLabel(item.observed.state)}</td>
      <td className="px-4 py-3 text-xs">
        {operation ? <span className="inline-flex items-center gap-2"><span className={`h-2 w-2 rounded-full ${stateTone(operation.state)}`} />{stateLabel(operation.kind)} · {stateLabel(operation.state)}</span> : "—"}
      </td>
      <td className="px-4 py-3 text-right font-mono text-[10px] text-muted-foreground">g{item.desired.generation}</td>
    </tr>
  );
}

export function ManagedServiceInstancesScreen() {
  const scope = useConsoleQueryScope();
  const { checkPermission, renderContext, profile } = useUserSession();
  const { activeWorkspaceID, loading: workspaceLoading } = useWorkspace();
  const personal = renderContext?.is_personal ?? true;
  const queryKey = useMemo(() => [...scope, "managed-services", "instances"] as const, [scope]);
  const realtimeKeys = useMemo(() => [queryKey] as const, [queryKey]);
  const instances = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam, signal }) => listManagedServiceInstances(personal, pageParam, signal),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
    enabled: Boolean(activeWorkspaceID) && !workspaceLoading && Boolean(renderContext),
    staleTime: 15_000,
    retry: (failureCount, error) => isAPIError(error) && error.retryable && failureCount < 2,
  });
  useManagedServiceRealtime(realtimeKeys);
  const items = instances.data?.pages.flatMap((page) => page.items) ?? [];
  const scopeLabel = personal ? "personal" : "tenant";
  const locale = profile?.locale || "en";

  return (
    <section className="w-full pb-10 text-foreground">
      <header className="flex flex-col gap-4 border-b border-border pb-5 sm:flex-row sm:items-end sm:justify-between">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-[6px] border border-violet-500/20 bg-violet-600/10 text-violet-500"><Boxes className="h-5 w-5" /></div>
          <div><h1 className="text-xl font-bold tracking-tight">Managed instances</h1><p className="mt-0.5 text-xs text-muted-foreground">{scopeLabel} services in the verified workspace and Zone{locale ? ` · ${locale}` : ""}.</p></div>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => void instances.refetch()} disabled={instances.isFetching}><RefreshCw className={`mr-2 h-3.5 w-3.5 ${instances.isFetching ? "animate-spin" : ""}`} />Refresh</Button>
          <Link className={buttonVariants({ variant: "outline", size: "sm" })} href="/managed-services/catalog">Catalog</Link>
          {checkPermission("managed-service:instance", "write") ? <Link className={buttonVariants({ size: "sm" })} href="/managed-services/new"><Plus className="mr-2 h-3.5 w-3.5" />Create service</Link> : null}
        </div>
      </header>

      {!activeWorkspaceID && !workspaceLoading ? <div className="mt-6 rounded-[6px] border border-dashed p-8 text-center text-sm text-muted-foreground">Select a workspace before opening managed instances.</div> : instances.isError ? (
        <div className="mt-6 rounded-[6px] border border-red-500/30 bg-red-500/5 p-5 text-sm text-red-600"><p className="font-semibold">Instances could not be loaded.</p><Button variant="outline" size="sm" className="mt-3" onClick={() => void instances.refetch()}>Try again</Button></div>
      ) : (
        <div className="mt-6 overflow-x-auto rounded-[6px] border border-border">
          <table className="w-full min-w-[760px] text-left text-sm">
            <thead className="border-b border-border bg-muted/40 text-xs text-muted-foreground"><tr><th className="px-4 py-3">Instance</th><th className="px-4 py-3">Desired</th><th className="px-4 py-3">Observed</th><th className="px-4 py-3">Latest operation</th><th className="px-4 py-3 text-right">Generation</th></tr></thead>
            <tbody>
              {instances.isLoading ? <tr><td colSpan={5} className="px-4 py-10 text-center text-muted-foreground">Loading instances…</td></tr> : null}
              {!instances.isLoading && items.length === 0 ? <tr><td colSpan={5} className="px-4 py-10 text-center text-muted-foreground">No managed instance exists in this workspace.</td></tr> : null}
              {items.map((item) => <InstanceRow key={item.id} item={item} />)}
            </tbody>
          </table>
          {instances.hasNextPage ? <div className="border-t border-border p-3 text-center"><Button variant="outline" size="sm" disabled={instances.isFetchingNextPage} onClick={() => void instances.fetchNextPage()}>{instances.isFetchingNextPage ? "Loading…" : "Load more"}</Button></div> : null}
        </div>
      )}
    </section>
  );
}
