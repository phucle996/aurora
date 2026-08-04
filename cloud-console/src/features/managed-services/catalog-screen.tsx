"use client";

import Link from "next/link";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Boxes, Plus, RefreshCw } from "lucide-react";

import { Button, buttonVariants } from "@/components/ui/button";
import { listManagedServiceCatalog, localizedText } from "@/features/managed-services/api";
import { useConsoleQueryScope } from "@/shared/query/scope";
import { useUserSession } from "@/session/use-session";
import { useWorkspace } from "@/context/WorkspaceContext";
import { isAPIError } from "@/shared/api/http";

export function ManagedServicesCatalogScreen() {
  const scope = useConsoleQueryScope();
  const { checkPermission, renderContext, profile } = useUserSession();
  const { activeWorkspaceID, loading: workspaceLoading } = useWorkspace();
  const consoleRoot = renderContext ? `/${renderContext.kind}` : "";
  const catalog = useInfiniteQuery({
    queryKey: [...scope, "managed-services", "catalog"],
    queryFn: ({ pageParam, signal }) => listManagedServiceCatalog(pageParam, signal),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
    enabled: Boolean(activeWorkspaceID) && !workspaceLoading && Boolean(renderContext),
    staleTime: 30_000,
    retry: (failureCount, error) => isAPIError(error) && error.retryable && failureCount < 2,
  });
  const items = catalog.data?.pages.flatMap((page) => page.items) ?? [];
  const locale = profile?.locale || "en";

  return (
    <div className="w-full pb-10 text-foreground">
      <header className="flex flex-col gap-4 border-b border-border pb-5 sm:flex-row sm:items-end sm:justify-between">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-[6px] border border-violet-500/20 bg-violet-600/10 text-violet-500">
            <Boxes className="h-5 w-5" />
          </div>
          <div>
            <h1 className="text-xl font-bold tracking-tight">Managed Services</h1>
            <p className="mt-0.5 text-xs text-muted-foreground">Provisionable services for the active workspace and Zone.</p>
          </div>
        </div>
        <div className="flex gap-2">
          <Link className={buttonVariants({ variant: "outline", size: "sm" })} href={`${consoleRoot}/managed-services`}>Instances</Link>
          <Button variant="outline" size="sm" onClick={() => void catalog.refetch()} disabled={catalog.isFetching}>
            <RefreshCw className={`mr-2 h-3.5 w-3.5 ${catalog.isFetching ? "animate-spin" : ""}`} />Refresh
          </Button>
          {activeWorkspaceID && items.length > 0 && checkPermission("managed-service:instance", "write") ? <Link className={buttonVariants({ size: "sm" })} href={`${consoleRoot}/managed-services/new`}><Plus className="mr-2 h-3.5 w-3.5" />Configure service</Link> : null}
        </div>
      </header>

      {!activeWorkspaceID && !workspaceLoading ? (
        <div className="mt-6 rounded-[6px] border border-dashed p-8 text-center text-sm text-muted-foreground">Select a workspace before opening the catalog.</div>
      ) : catalog.isError ? (
        <div className="mt-6 rounded-[6px] border border-red-500/30 bg-red-500/5 p-5 text-sm text-red-600">The catalog is unavailable. Retry after the current workspace and Zone are verified.</div>
      ) : (
        <div className="mt-6 overflow-hidden rounded-[6px] border border-border">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-border bg-muted/40 text-xs text-muted-foreground">
              <tr><th className="px-4 py-3">Category</th><th className="px-4 py-3">Service</th><th className="px-4 py-3">Version</th><th className="px-4 py-3">Contract</th></tr>
            </thead>
            <tbody>
              {catalog.isLoading ? <tr><td colSpan={4} className="px-4 py-8 text-center text-muted-foreground">Loading catalog…</td></tr> : null}
              {!catalog.isLoading && items.length === 0 ? <tr><td colSpan={4} className="px-4 py-8 text-center text-muted-foreground">No published service is provisionable in this Zone.</td></tr> : null}
              {items.map((item) => (
                <tr key={item.version.id} className="border-b border-border last:border-0">
                  <td className="px-4 py-3 text-muted-foreground">{localizedText(item.category.name_i18n, locale) || item.category.code}</td>
                  <td className="px-4 py-3"><div className="font-medium">{localizedText(item.definition.name_i18n, locale) || item.definition.code}</div><div className="text-xs text-muted-foreground">{item.definition.code}</div></td>
                  <td className="px-4 py-3">{item.version.display_version}</td>
                  <td className="px-4 py-3 font-mono text-xs">r{item.revision.number}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {catalog.hasNextPage ? (
            <div className="border-t border-border p-3 text-center">
              <Button variant="outline" size="sm" disabled={catalog.isFetchingNextPage} onClick={() => void catalog.fetchNextPage()}>
                {catalog.isFetchingNextPage ? "Loading…" : "Load more"}
              </Button>
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}
