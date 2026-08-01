"use client";

import { useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams, useRouter } from "next/navigation";
import { ArrowLeft, ExternalLink, RefreshCw, RotateCcw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  deleteManagedServiceInstance,
  getManagedServiceInstance,
  listManagedServiceOperations,
  localizedText,
  renameManagedServiceInstance,
  resizeManagedServiceInstance,
  retryManagedServiceOperation,
} from "@/features/managed-services/api";
import { useManagedServiceRealtime } from "@/features/managed-services/realtime";
import type { FormDraftValue, ManagedServiceOperation } from "@/features/managed-services/model";
import { ManagedServiceContractField } from "@/features/managed-services/form";
import { useConsoleQueryScope } from "@/shared/query/scope";
import { useUserSession } from "@/session/use-session";
import { isAPIError } from "@/shared/api/http";
import { useWorkspace } from "@/context/WorkspaceContext";

function stateLabel(value: string): string {
  return value.replaceAll("_", " ").toLowerCase().replace(/(^|\s)\S/g, (letter) => letter.toUpperCase());
}

function stateTone(value: string): string {
  if (value === "active" || value === "succeeded") return "bg-emerald-500";
  if (value === "failed" || value === "terminal_failed") return "bg-red-500";
  return "bg-amber-500";
}

function StatePill({ value }: { value: string }) {
  return <span className="inline-flex items-center gap-2 text-xs font-semibold"><span className={`h-2 w-2 rounded-full ${stateTone(value)}`} />{stateLabel(value)}</span>;
}

function OperationRow({ operation, onRetry, retrying }: { operation: ManagedServiceOperation; onRetry: () => void; retrying: boolean }) {
  return (
    <div className="grid gap-3 rounded-[6px] border border-border p-4 md:grid-cols-[minmax(0,1fr)_150px_150px_auto] md:items-center">
      <div className="min-w-0"><p className="font-semibold">{stateLabel(operation.kind)}</p><p className="truncate font-mono text-[10px] text-muted-foreground">{operation.id}</p>{operation.last_sanitized_error ? <p className="mt-1 text-xs text-red-600">{operation.last_sanitized_error}</p> : null}</div>
      <StatePill value={operation.state} />
      <span className="text-xs text-muted-foreground">attempt {operation.attempt} · g{operation.generation}</span>
      {operation.state === "terminal_failed" ? <Button variant="outline" size="sm" disabled={retrying} onClick={onRetry}><RotateCcw className="mr-1.5 h-3.5 w-3.5" />{retrying ? "Retrying…" : "Retry"}</Button> : <span />}
    </div>
  );
}

export function ManagedServiceInstanceDetailScreen() {
  const params = useParams<{ code: string }>();
  const router = useRouter();
  const queryClient = useQueryClient();
  const scope = useConsoleQueryScope();
  const { checkPermission, renderContext, profile } = useUserSession();
  const { activeWorkspaceID, loading: workspaceLoading } = useWorkspace();
  const personal = renderContext?.is_personal ?? true;
  const canWrite = checkPermission("managed-service:instance", "write");
  const locale = profile?.locale || "en";
  const code = typeof params.code === "string" ? params.code : "";
  const instanceKey = useMemo(() => [...scope, "managed-services", "instance", code] as const, [scope, code]);
  const operationsKey = useMemo(() => [...scope, "managed-services", "instance", code, "operations"] as const, [scope, code]);
  const instancesKey = useMemo(() => [...scope, "managed-services", "instances"] as const, [scope]);
  const realtimeKeys = useMemo(() => [instanceKey, operationsKey, instancesKey] as const, [instanceKey, instancesKey, operationsKey]);
  const instanceQuery = useQuery({
    queryKey: instanceKey,
    queryFn: ({ signal }) => getManagedServiceInstance(personal, code, signal),
    enabled: Boolean(activeWorkspaceID) && !workspaceLoading && Boolean(renderContext) && Boolean(code),
    staleTime: 10_000,
    retry: (failureCount, error) => isAPIError(error) && error.retryable && failureCount < 2,
  });
  const operationsQuery = useInfiniteQuery({
    queryKey: operationsKey,
    queryFn: ({ pageParam, signal }) => listManagedServiceOperations(personal, code, pageParam, signal),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
    enabled: instanceQuery.isSuccess,
    staleTime: 10_000,
    retry: (failureCount, error) => isAPIError(error) && error.retryable && failureCount < 2,
  });
  useManagedServiceRealtime(realtimeKeys);

  const [name, setName] = useState("");
  const [resizeValues, setResizeValues] = useState<Record<string, FormDraftValue>>({});
  const instance = instanceQuery.data;
  const operationRows = operationsQuery.data?.pages.flatMap((page) => page.items) ?? [];
  const resizeGroups = useMemo(() => [...(instance?.resize_contract?.ui_schema.groups ?? [])].sort((left, right) => left.order - right.order), [instance?.resize_contract]);

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: instanceKey });
    void queryClient.invalidateQueries({ queryKey: operationsKey });
    void queryClient.invalidateQueries({ queryKey: instancesKey });
  };

  const renameMutation = useMutation({
    mutationFn: () => {
      if (!instance) throw new Error("Instance is not loaded.");
      const nextName = name.trim();
      if (!nextName || nextName.length > 160) throw new Error("Enter a valid display name.");
      return renameManagedServiceInstance(personal, code, { name: nextName, expected_metadata_version: instance.metadata_version });
    },
    onSuccess: () => { setName(""); invalidate(); toast.success("Instance name updated."); },
    onError: (error: unknown) => toast.error(error instanceof Error ? error.message : "Rename failed."),
  });

  const resizeMutation = useMutation({
    mutationFn: () => {
      if (!instance) throw new Error("Instance is not loaded.");
      const contract = instance.resize_contract;
      if (!contract) throw new Error("This revision does not expose a resize contract.");
      const resources: Record<string, FormDraftValue> = {};
      for (const field of contract.input_schema.fields.filter((candidate) => candidate.mutable)) {
        const value = resizeValues[field.key];
        if (value === undefined || value === "" || (Array.isArray(value) && value.length === 0)) {
          if (field.required) throw new Error(`Complete required field ${field.key}.`);
          continue;
        }
        if ((field.cardinality === "ONE" && Array.isArray(value)) || (field.cardinality !== "ONE" && !Array.isArray(value))) throw new Error(`Field ${field.key} has an invalid cardinality.`);
        const values = Array.isArray(value) ? value : [value];
        if ((field.min_items !== undefined && values.length < field.min_items) || (field.max_items !== undefined && values.length > field.max_items)) throw new Error(`Field ${field.key} has an invalid item count.`);
        if (field.cardinality === "SET" && new Set(values.map((item) => `${typeof item}:${String(item)}`)).size !== values.length) throw new Error(`Field ${field.key} contains duplicate values.`);
        for (const item of values) {
          if (field.value_type === "BOOLEAN" && typeof item !== "boolean") throw new Error(`Field ${field.key} must be boolean.`);
          if (field.value_type === "INT64" && (typeof item !== "number" || !Number.isSafeInteger(item) || (field.min !== undefined && item < field.min) || (field.max !== undefined && item > field.max))) throw new Error(`Field ${field.key} must be a valid integer.`);
          if ((field.value_type === "DECIMAL" || field.value_type === "PORT") && (typeof item !== "number" || !Number.isFinite(item) || (field.value_type === "PORT" && (!Number.isSafeInteger(item) || item < 1 || item > 65535)) || (field.min !== undefined && item < field.min) || (field.max !== undefined && item > field.max))) throw new Error(`Field ${field.key} must be a valid number.`);
          if (typeof item === "string") {
            if ((field.min_length !== undefined && item.length < field.min_length) || (field.max_length !== undefined && item.length > field.max_length)) throw new Error(`Field ${field.key} has an invalid length.`);
            if (field.value_type === "ENUM" && !(field.enum_values ?? []).includes(item)) throw new Error(`Field ${field.key} contains an unsupported option.`);
            if (field.value_type === "DNS_LABEL" && !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(item)) throw new Error(`Field ${field.key} must be a DNS label.`);
          }
        }
        resources[field.key] = value;
      }
      if (Object.keys(resources).length === 0) throw new Error("No mutable resize input is available for this revision.");
      return resizeManagedServiceInstance(personal, code, { expected_generation: instance.desired.generation, resources });
    },
    onSuccess: () => { setResizeValues({}); invalidate(); toast.success("Resize accepted; waiting for the durable operation result."); },
    onError: (error: unknown) => toast.error(error instanceof Error ? error.message : "Resize failed."),
  });

  const deleteMutation = useMutation({
    mutationFn: () => {
      if (!instance) throw new Error("Instance is not loaded.");
      return deleteManagedServiceInstance(personal, code, instance.desired.generation);
    },
    onSuccess: () => { invalidate(); toast.success("Delete accepted; the instance remains DELETING until Zone confirmation."); },
    onError: (error: unknown) => toast.error(error instanceof Error ? error.message : "Delete failed."),
  });

  const retryMutation = useMutation({
    mutationFn: (operationID: string) => retryManagedServiceOperation(personal, code, operationID),
    onSuccess: () => { invalidate(); toast.success("Retry accepted."); },
    onError: (error: unknown) => toast.error(error instanceof Error ? error.message : "Retry failed."),
  });

  if (!activeWorkspaceID && !workspaceLoading) return <div className="rounded-[6px] border border-dashed p-8 text-center text-sm text-muted-foreground">Select a workspace before opening an instance.</div>;
  if (instanceQuery.isLoading) return <div className="p-12 text-center text-sm text-muted-foreground">Loading managed instance…</div>;
  if (instanceQuery.isError || !instance) return <div className="rounded-[6px] border border-red-500/30 bg-red-500/5 p-6 text-sm text-red-600"><p className="font-semibold">Managed instance could not be loaded.</p><Button variant="outline" size="sm" className="mt-3" onClick={() => void instanceQuery.refetch()}>Try again</Button></div>;

  return (
    <div className="w-full pb-10 text-foreground">
      <header className="flex flex-col gap-4 border-b border-border pb-5 sm:flex-row sm:items-end sm:justify-between">
        <div className="flex items-start gap-3"><Button variant="outline" size="icon" className="h-8 w-8" onClick={() => router.push("/managed-services")}><ArrowLeft className="h-4 w-4" /></Button><div><p className="font-mono text-[10px] text-muted-foreground">{instance.code}</p><h1 className="text-xl font-bold tracking-tight">{instance.name}</h1><div className="mt-1 flex flex-wrap items-center gap-3"><StatePill value={instance.desired.state} /><span className="text-xs text-muted-foreground">observed {stateLabel(instance.observed.state)}</span></div></div></div>
        <div className="flex gap-2"><Button variant="outline" size="sm" onClick={() => { void instanceQuery.refetch(); void operationsQuery.refetch(); }} disabled={instanceQuery.isFetching || operationsQuery.isFetching}><RefreshCw className={`mr-2 h-3.5 w-3.5 ${instanceQuery.isFetching || operationsQuery.isFetching ? "animate-spin" : ""}`} />Refresh</Button>{canWrite ? <Button variant="destructive" size="sm" disabled={deleteMutation.isPending || instance.desired.state === "deleting" || instance.desired.state === "deleted"} onClick={() => { if (window.confirm(`Delete ${instance.name}? The instance will remain DELETING until the Zone confirms removal.`)) deleteMutation.mutate(); }}><Trash2 className="mr-2 h-3.5 w-3.5" />Delete</Button> : null}</div>
      </header>

      <Tabs defaultValue="overview" className="mt-6">
        <TabsList variant="line" className="w-full justify-start border-b border-border"><TabsTrigger value="overview">Overview</TabsTrigger><TabsTrigger value="configuration">Configuration</TabsTrigger><TabsTrigger value="connection">Safe Connection</TabsTrigger><TabsTrigger value="operations">Operations</TabsTrigger></TabsList>
        <TabsContent value="overview" className="mt-5 space-y-5">
          <div className="grid gap-4 md:grid-cols-3"><div className="rounded-[6px] border border-border p-5"><p className="text-xs text-muted-foreground">Desired</p><p className="mt-2 text-lg font-semibold">{stateLabel(instance.desired.state)}</p><p className="mt-1 text-xs text-muted-foreground">generation {instance.desired.generation}</p></div><div className="rounded-[6px] border border-border p-5"><p className="text-xs text-muted-foreground">Observed</p><p className="mt-2 text-lg font-semibold">{stateLabel(instance.observed.state)}</p><p className="mt-1 text-xs text-muted-foreground">version {instance.observed.version}</p></div><div className="rounded-[6px] border border-border p-5"><p className="text-xs text-muted-foreground">Latest operation</p><p className="mt-2 text-lg font-semibold">{instance.latest_operation ? stateLabel(instance.latest_operation.kind) : "None"}</p><p className="mt-1 text-xs text-muted-foreground">{instance.latest_operation ? stateLabel(instance.latest_operation.state) : "No operation recorded"}</p></div></div>
          {canWrite ? <div className="rounded-[6px] border border-border p-5"><h2 className="text-sm font-semibold">Rename</h2><p className="mt-1 text-xs text-muted-foreground">Only display metadata changes; code and revision remain immutable.</p><div className="mt-4 flex max-w-xl gap-2"><Label htmlFor="instance-name" className="sr-only">New display name</Label><Input id="instance-name" value={name} maxLength={160} placeholder={instance.name} onChange={(event) => setName(event.target.value)} /><Button disabled={renameMutation.isPending || !name.trim()} onClick={() => renameMutation.mutate()}>{renameMutation.isPending ? "Saving…" : "Save name"}</Button></div></div> : null}
        </TabsContent>
        <TabsContent value="configuration" className="mt-5 space-y-5">
          <div className="rounded-[6px] border border-border p-5"><h2 className="text-sm font-semibold">Pinned desired state</h2><dl className="mt-4 grid gap-3 text-sm sm:grid-cols-2"><dt className="text-muted-foreground">Active revision</dt><dd className="break-all font-mono text-xs">{instance.desired.active_revision_id ?? "Pending"}</dd><dt className="text-muted-foreground">Pending revision</dt><dd className="break-all font-mono text-xs">{instance.desired.pending_revision_id ?? "None"}</dd><dt className="text-muted-foreground">Revision sequence</dt><dd>{instance.desired.revision_sequence}</dd><dt className="text-muted-foreground">Metadata version</dt><dd>{instance.metadata_version}</dd></dl></div>
          {canWrite ? <div className="rounded-[6px] border border-border p-5"><h2 className="text-sm font-semibold">Resize</h2><p className="mt-1 text-xs text-muted-foreground">The pinned SRE contract defines mutable inputs. Values start empty and are never copied from protected payloads.</p>{instance.resize_contract ? <div className="mt-4 space-y-4">{resizeGroups.map((group) => { const fields = instance.resize_contract?.ui_schema.fields.filter((field) => field.group === group.key && instance.resize_contract?.input_schema.fields.some((input) => input.key === field.key && input.mutable)).sort((left, right) => left.order - right.order) ?? []; return fields.length > 0 ? <div key={group.key} className="rounded border border-border/70 p-4"><p className="text-xs font-semibold">{localizedText(group.label_i18n, locale) || group.key}</p><div className="mt-3 grid gap-4 sm:grid-cols-2">{fields.map((uiField) => { const input = instance.resize_contract?.input_schema.fields.find((candidate) => candidate.key === uiField.key); return input ? <ManagedServiceContractField key={input.key} input={input} ui={uiField} locale={locale} value={resizeValues[input.key]} onChange={(value) => setResizeValues((current) => ({ ...current, [input.key]: value }))} /> : null; })}</div></div> : null; })}<Button disabled={resizeMutation.isPending || instance.desired.state !== "active"} onClick={() => resizeMutation.mutate()}>{resizeMutation.isPending ? "Submitting…" : "Submit resize"}</Button></div> : <p className="mt-4 text-sm text-muted-foreground">This revision does not expose a resize contract.</p>}{instance.desired.state !== "active" ? <p className="mt-3 text-xs text-amber-600">Resize is available only while the desired lifecycle is active.</p> : null}</div> : null}
        </TabsContent>
        <TabsContent value="connection" className="mt-5 space-y-5">
          <div className="rounded-[6px] border border-border p-5"><h2 className="text-sm font-semibold">Zone service endpoints</h2><p className="mt-1 text-xs text-muted-foreground">Only approved service names and ports are shown. Secrets, raw YAML and protected parameters never reach this view.</p>{instance.network_contract ? <><p className="mt-4 font-mono text-xs">namespace: {instance.network_contract.namespace}</p><div className="mt-4 space-y-3">{instance.network_contract.components.map((component) => <div key={component.component_code} className="rounded border border-border/70 p-4"><div className="flex flex-wrap items-center justify-between gap-2"><span className="font-semibold">{component.component_code}</span><span className="font-mono text-xs text-muted-foreground">{component.service_name}</span></div><div className="mt-3 flex flex-wrap gap-2">{component.ports.map((port) => <span key={`${component.component_code}:${port.name}:${port.port}`} className="rounded bg-muted px-2 py-1 font-mono text-[11px]">{port.name} · {port.port}/{port.protocol}</span>)}</div></div>)}</div></> : <p className="mt-5 text-sm text-muted-foreground">Safe connection output is not available yet. Wait for a Zone result.</p>}</div>
          <div className="rounded-[6px] border border-dashed border-border p-5 text-xs text-muted-foreground"><ExternalLink className="mr-2 inline h-3.5 w-3.5" />Connection launch remains disabled until the approved Zone gateway contract is present.</div>
        </TabsContent>
        <TabsContent value="operations" className="mt-5 space-y-3">
          {operationsQuery.isError ? <div className="rounded-[6px] border border-red-500/30 bg-red-500/5 p-5 text-sm text-red-600">Operation history is temporarily unavailable. <Button variant="outline" size="sm" className="ml-2" onClick={() => void operationsQuery.refetch()}>Retry</Button></div> : null}
          {operationsQuery.isLoading ? <div className="p-8 text-center text-sm text-muted-foreground">Loading operation history…</div> : null}
          {!operationsQuery.isLoading && operationRows.length === 0 ? <div className="rounded-[6px] border border-dashed p-8 text-center text-sm text-muted-foreground">No operation history.</div> : null}
          {operationRows.map((operation) => <OperationRow key={operation.id} operation={operation} retrying={!canWrite || retryMutation.isPending} onRetry={() => { if (canWrite) retryMutation.mutate(operation.id); }} />)}
          {operationsQuery.hasNextPage ? <div className="pt-2 text-center"><Button variant="outline" size="sm" disabled={operationsQuery.isFetchingNextPage} onClick={() => void operationsQuery.fetchNextPage()}>Load more</Button></div> : null}
        </TabsContent>
      </Tabs>
    </div>
  );
}
