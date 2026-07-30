"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { ArrowLeft, Boxes } from "lucide-react";
import { toast } from "sonner";

import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { getManagedServiceVersionContract, listManagedServiceCatalog, localizedText } from "@/features/managed-services/api";
import { ManagedServiceContractField } from "@/features/managed-services/form";
import type { FormDraftValue } from "@/features/managed-services/model";
import { useConsoleQueryScope } from "@/shared/query/scope";
import { useUserSession } from "@/session/use-session";
import { useWorkspace } from "@/context/WorkspaceContext";
import { isAPIError } from "@/shared/api/http";

export function CreateManagedServiceScreen() {
  const scope = useConsoleQueryScope();
  const scopeFence = scope.join(":");
  const { renderContext, profile } = useUserSession();
  const { activeWorkspaceID } = useWorkspace();
  const personal = renderContext?.is_personal ?? true;
  const locale = profile?.locale || "en";
  const [selectedVersionID, setSelectedVersionID] = useState("");

  const catalog = useInfiniteQuery({
    queryKey: [...scope, "managed-services", "catalog"],
    queryFn: ({ pageParam, signal }) => listManagedServiceCatalog(personal, pageParam, signal),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
    enabled: Boolean(activeWorkspaceID) && Boolean(renderContext),
    staleTime: 30_000,
    retry: (failureCount, error) => isAPIError(error) && error.retryable && failureCount < 2,
  });
  const items = catalog.data?.pages.flatMap((page) => page.items) ?? [];
  const selected = items.find((item) => item.version.id === selectedVersionID) ?? items[0];
  const contract = useQuery({
    queryKey: [...scope, "managed-services", "version", selected?.version.id ?? "none", selected?.revision.id ?? "none"],
    queryFn: ({ signal }) => getManagedServiceVersionContract(personal, selected!.version.id, selected!.revision.id, signal),
    enabled: Boolean(selected),
    retry: (failureCount, error) => isAPIError(error) && error.retryable && failureCount < 2,
  });
  // A refetch is a revision-fence check. Never keep an older successful form
  // actionable while the backend is deciding whether its default is stale.
  const formContract = contract.isError || contract.isFetching ? undefined : contract.data;
  const currentDraftFence = `${scopeFence}:${selected?.revision.id ?? "none"}`;
  const [storedDraft, setStoredDraft] = useState<{
    fence: string;
    step: "configure" | "review";
    code: string;
    name: string;
    values: Record<string, FormDraftValue>;
  }>({ fence: "", step: "configure", code: "", name: "", values: {} });
  // Security invariant: stale state remains unreachable when auth, owner,
  // workspace, Zone or revision changes. The first edit creates a new fenced
  // draft; no effect copies the previous parameter document across scopes.
  const draft = storedDraft.fence === currentDraftFence
    ? storedDraft
    : { fence: currentDraftFence, step: "configure" as const, code: "", name: "", values: {} as Record<string, FormDraftValue> };

  const orderedGroups = useMemo(
    () => [...(formContract?.ui_schema.groups ?? [])].sort((left, right) => left.order - right.order),
    [formContract],
  );

  if (!activeWorkspaceID) {
    return <div className="rounded-[6px] border border-dashed p-8 text-center text-sm text-muted-foreground">Select a workspace before configuring a service.</div>;
  }

  return (
    <div className="w-full pb-10 text-foreground">
      <header className="flex items-center gap-3 border-b border-border pb-5">
        <Link aria-label="Back to Managed Services" className={buttonVariants({ variant: "outline", size: "icon", className: "h-8 w-8" })} href="/managed-services"><ArrowLeft className="h-4 w-4" /></Link>
        <div className="flex h-9 w-9 items-center justify-center rounded-[6px] border border-violet-500/20 bg-violet-600/10 text-violet-500"><Boxes className="h-4.5 w-4.5" /></div>
        <div><h1 className="text-xl font-bold tracking-tight">Configure Managed Service</h1><p className="mt-0.5 text-xs text-muted-foreground">Catalog → configure → review. Workspace, Zone and revision are read-only.</p></div>
      </header>

      <div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-12">
        <section className="space-y-5 lg:col-span-8">
          <div className="rounded-[6px] border border-border">
            <div className="border-b border-border px-5 py-3"><h2 className="text-sm font-semibold">Catalog selection</h2></div>
            <div className="grid gap-5 p-5 sm:grid-cols-2">
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="managed-service-version">Application and version</Label>
                <select id="managed-service-version" value={selected?.version.id ?? ""} onChange={(event) => setSelectedVersionID(event.target.value)} disabled={catalog.isLoading || items.length === 0} className="flex h-9 w-full rounded-md border border-input bg-background px-3 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring">
                  {items.map((item) => <option key={item.version.id} value={item.version.id}>{localizedText(item.definition.name_i18n, locale) || item.definition.code} · {item.version.display_version}</option>)}
                </select>
                {catalog.hasNextPage ? <Button type="button" variant="outline" size="sm" disabled={catalog.isFetchingNextPage} onClick={() => void catalog.fetchNextPage()}>{catalog.isFetchingNextPage ? "Loading…" : "Load more versions"}</Button> : null}
              </div>
              <div className="space-y-2"><Label htmlFor="managed-service-code">Code</Label><Input id="managed-service-code" value={draft.code} maxLength={35} placeholder="orders-kafka" onChange={(event) => setStoredDraft({ ...draft, code: event.target.value.toLowerCase() })} required /></div>
              <div className="space-y-2"><Label htmlFor="managed-service-name">Display name</Label><Input id="managed-service-name" value={draft.name} maxLength={160} placeholder="Orders Kafka" onChange={(event) => setStoredDraft({ ...draft, name: event.target.value })} required /></div>
            </div>
          </div>

          {contract.isError ? <div className="rounded-[6px] border border-red-500/30 bg-red-500/5 p-5 text-sm text-red-600">The form contract is stale or unsupported. Return to the catalog and refresh.</div> : null}
          {contract.isLoading ? <div className="rounded-[6px] border border-border p-8 text-center text-sm text-muted-foreground">Loading immutable form contract…</div> : null}
          {formContract && draft.step === "configure" ? orderedGroups.map((group) => {
            const fields = formContract.ui_schema.fields.filter((field) => field.group === group.key).sort((left, right) => left.order - right.order);
            return (
              <div key={group.key} className="rounded-[6px] border border-border">
                <div className="border-b border-border px-5 py-3"><h2 className="text-sm font-semibold">{localizedText(group.label_i18n, locale) || group.key}</h2></div>
                <div className="grid gap-5 p-5 sm:grid-cols-2">
                  {fields.map((uiField) => {
                    const input = formContract.input_schema.fields.find((field) => field.key === uiField.key);
                    if (!input) return null;
                    return <ManagedServiceContractField key={input.key} input={input} ui={uiField} locale={locale} value={draft.values[input.key]} onChange={(value) => setStoredDraft({ ...draft, values: { ...draft.values, [input.key]: value } })} />;
                  })}
                </div>
              </div>
            );
          }) : null}

          {formContract && draft.step === "review" ? (
            <div className="rounded-[6px] border border-border">
              <div className="border-b border-border px-5 py-3"><h2 className="text-sm font-semibold">Review create intent</h2></div>
              <dl className="grid grid-cols-[160px_1fr] gap-x-4 gap-y-3 p-5 text-sm">
                <dt className="text-muted-foreground">Service</dt><dd>{localizedText(formContract.definition.name_i18n, locale) || formContract.definition.code}</dd>
                <dt className="text-muted-foreground">Version</dt><dd>{formContract.version.display_version}</dd>
                <dt className="text-muted-foreground">Revision</dt><dd className="font-mono">{formContract.revision.id}</dd>
                <dt className="text-muted-foreground">Code</dt><dd className="font-mono">{draft.code}</dd>
                <dt className="text-muted-foreground">Name</dt><dd>{draft.name}</dd>
                <dt className="text-muted-foreground">Input</dt><dd><pre className="max-h-64 overflow-auto rounded bg-muted p-3 text-xs">{JSON.stringify(draft.values, null, 2)}</pre></dd>
              </dl>
            </div>
          ) : null}
        </section>

        <aside className="self-start rounded-[6px] border border-border lg:col-span-4">
          <div className="border-b border-border px-5 py-3"><h2 className="text-sm font-semibold">Verified context</h2></div>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-3 p-5 text-[13px]">
            <dt className="text-muted-foreground">Owner mode</dt><dd className="text-right">{personal ? "Personal" : "Tenant"}</dd>
            <dt className="text-muted-foreground">Workspace</dt><dd className="truncate text-right font-mono text-xs">{activeWorkspaceID}</dd>
            <dt className="text-muted-foreground">Revision</dt><dd className="truncate text-right font-mono text-xs">{selected?.revision.id ?? "—"}</dd>
          </dl>
          <div className="space-y-2 border-t border-border p-4">
            {draft.step === "configure" ? (
              <Button className="w-full" disabled={!formContract || !/^[a-z](?:[a-z0-9-]{0,33}[a-z0-9])?$/.test(draft.code) || !draft.name.trim()} onClick={() => {
                const missing = formContract?.input_schema.fields.some((field) => {
                  const value = draft.values[field.key];
                  return field.required && (value === undefined || value === "" || (Array.isArray(value) && value.length === 0));
                });
                if (missing) { toast.error("Complete every required field before review."); return; }
                const invalid = formContract?.input_schema.fields.some((field) => {
                  const value = draft.values[field.key];
                  if (value === undefined || value === "" || (Array.isArray(value) && value.length === 0)) return false;
                  if ((field.cardinality === "ONE" && Array.isArray(value)) || (field.cardinality !== "ONE" && !Array.isArray(value))) return true;
                  const values = Array.isArray(value) ? value : [value];
                  if ((field.min_items !== undefined && values.length < field.min_items) || (field.max_items !== undefined && values.length > field.max_items)) return true;
                  if (field.cardinality === "SET" && new Set(values.map((item) => `${typeof item}:${String(item)}`)).size !== values.length) return true;
                  return values.some((item) => {
                    if (field.value_type === "BOOLEAN") return typeof item !== "boolean";
                    if (field.value_type === "INT64") {
                      return typeof item !== "number" || !Number.isSafeInteger(item) || (field.min !== undefined && item < field.min) || (field.max !== undefined && item > field.max);
                    }
                    if (field.value_type === "DECIMAL") {
                      return typeof item !== "number" || !Number.isFinite(item) || (field.min !== undefined && item < field.min) || (field.max !== undefined && item > field.max);
                    }
                    if (field.value_type === "PORT") {
                      return typeof item !== "number" || !Number.isSafeInteger(item) || item < 1 || item > 65535 || (field.min !== undefined && item < field.min) || (field.max !== undefined && item > field.max);
                    }
                    if (typeof item !== "string") return true;
                    if ((field.min_length !== undefined && item.length < field.min_length) || (field.max_length !== undefined && item.length > field.max_length)) return true;
                    if (field.value_type === "ENUM") return !(field.enum_values ?? []).includes(item);
                    if (field.value_type === "DNS_LABEL") return !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(item);
                    if (field.value_type === "CIDR") {
                      const [address, prefix, extra] = item.split("/");
                      if (extra !== undefined || prefix === undefined || !/^\d+$/.test(prefix)) return true;
                      const prefixNumber = Number(prefix);
                      if (address.includes(":")) return !/^[0-9a-fA-F:]+$/.test(address) || prefixNumber < 0 || prefixNumber > 128;
                      const octets = address.split(".");
                      return octets.length !== 4 || octets.some((octet) => !/^\d{1,3}$/.test(octet) || Number(octet) > 255) || prefixNumber < 0 || prefixNumber > 32;
                    }
                    return false;
                  });
                });
                if (invalid) { toast.error("One or more values do not satisfy the published form contract."); return; }
                setStoredDraft({ ...draft, step: "review" });
              }}>Review</Button>
            ) : (
              <><Button variant="outline" className="w-full" onClick={() => setStoredDraft({ ...draft, step: "configure" })}>Back to configuration</Button><Button className="w-full" disabled>Provisioning opens in Phase 4</Button></>
            )}
          </div>
        </aside>
      </div>
    </div>
  );
}
