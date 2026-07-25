"use client";

import { FormEvent, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, Loader2, Pause, Pencil, Play, Plus, RefreshCw, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { type APIError } from "@/lib/api/fetcher";
import { changeMailConsumerState, createMailConsumer, deleteMailConsumer, getMailConsumer, listMailConsumers, watchMailConsumerRuntime, type ConsumerWrite, type MailConsumer, type MailConsumerRuntimeWatch, type MailRuntimeState, type MailSourceType, updateMailConsumer } from "@/lib/api/mail";
import { useRealtime } from "@/context/RealtimeContext";

type ConsumersTabProps = { enabled: boolean; scopeKey: string; canCreate: boolean; canUpdate: boolean; canDelete: boolean };
type ConsumerForm = ConsumerWrite & { code: string };
type MailConsumerJobNotification = { operation?: unknown; resource_id?: string; status?: string };
type MailConsumerRuntimeNotification = {
  scope?: unknown;
  consumer_id?: unknown;
  config_version?: unknown;
  runtime_epoch?: unknown;
  runtime_revision?: unknown;
  state?: unknown;
  active_instances?: unknown;
  consumer_lag?: unknown;
  error_code?: unknown;
  error_message?: unknown;
  observed_at?: unknown;
  expires_at?: unknown;
};

const emptyForm: ConsumerForm = {
  code: "", name: "", source_type: "kafka", broker_resource_id: "", topic: "", consumer_group: "",
  template_id: "", template_version: 1, sender_profile_id: "", sender_version: 1, parallelism: 1,
};

const sourceLabels: Record<MailSourceType, { name: string; source: string; consumer: string }> = {
  kafka: { name: "Kafka", source: "Kafka topic", consumer: "Consumer group" },
  redis_stream: { name: "Redis Stream", source: "Redis stream key", consumer: "Consumer group" },
  nats_jetstream: { name: "NATS JetStream", source: "JetStream stream", consumer: "Durable consumer" },
  rabbitmq: { name: "RabbitMQ", source: "RabbitMQ queue", consumer: "Consumer tag prefix" },
};

function errorMessage(error: unknown): string {
  const apiError = error as APIError;
  if (apiError?.status === 409) return "Configuration changed in another session. Reload the latest version before retrying.";
  return apiError?.message || (error instanceof Error ? error.message : "Request failed");
}

export function ConsumersTab({ enabled, scopeKey, canCreate, canUpdate, canDelete }: ConsumersTabProps) {
  const queryClient = useQueryClient();
  const { subscribeToStream } = useRealtime();
  const queryKey = ["mail", scopeKey, "consumers"] as const;
  const [search, setSearch] = useState("");
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<MailConsumer | null>(null);
  const [form, setForm] = useState<ConsumerForm>(emptyForm);
  const [detailConsumerID, setDetailConsumerID] = useState<string | null>(null);

  const consumers = useQuery({
    queryKey,
    queryFn: ({ signal }) => listMailConsumers(signal),
    enabled,
  });
  const consumerDetail = useQuery({
    queryKey: ["mail", scopeKey, "consumer-detail", detailConsumerID],
    queryFn: ({ signal }) => getMailConsumer(detailConsumerID as string, signal),
    enabled: enabled && Boolean(detailConsumerID),
  });
  const runtimeWatchKey = useMemo(
    () => ["mail", scopeKey, "consumer-runtime-watch", detailConsumerID] as const,
    [detailConsumerID, scopeKey],
  );
  const runtimeWatch = useQuery({
    queryKey: runtimeWatchKey,
    queryFn: ({ signal }) => watchMailConsumerRuntime(detailConsumerID as string, signal),
    enabled: enabled && Boolean(detailConsumerID),
    // [COMMENT]: Đây là lease renewal khi Detail còn mở, không phải status polling. Runtime delta
    // giữa hai lần renew đi qua Centrifugo và rời màn hình sẽ dừng request hoàn toàn.
    refetchInterval: 20_000,
    refetchIntervalInBackground: false,
    retry: false,
  });
  const visible = useMemo(() => {
    const needle = search.trim().toLowerCase();
    if (!needle) return consumers.data ?? [];
    return (consumers.data ?? []).filter((item) => [item.name, item.topic, item.consumer_group, item.template_id].some((value) => value.toLowerCase().includes(needle)));
  }, [consumers.data, search]);

  useEffect(() => {
    // [COMMENT]: Không poll status URL và không lưu audit ở UI; terminal Centrifugo signal chỉ merge lại read model liên quan.
    return subscribeToStream("job", "job.notification", (payload: MailConsumerJobNotification) => {
      if (typeof payload?.operation !== "string" || !payload.operation.startsWith("mail.consumer.") || !payload.resource_id || typeof payload.status !== "string" || !["SUCCESS", "FAILED"].includes(payload.status)) return;
      void queryClient.invalidateQueries({ queryKey: ["mail", scopeKey, "consumers"] });
      if (detailConsumerID === payload.resource_id) {
        void queryClient.invalidateQueries({ queryKey: ["mail", scopeKey, "consumer-detail", detailConsumerID] });
        // [COMMENT]: Promotion có thể đổi active config version; renew ngay để tạo epoch mới
        // thay vì giữ runtime generation cũ tới interval kế tiếp.
        void queryClient.invalidateQueries({ queryKey: runtimeWatchKey });
      }
    });
  }, [detailConsumerID, queryClient, runtimeWatchKey, scopeKey, subscribeToStream]);

  useEffect(() => {
    return subscribeToStream("runtime", "mail.consumer.runtime.changed", (payload: MailConsumerRuntimeNotification) => {
      if (
        !detailConsumerID ||
        payload.consumer_id !== detailConsumerID ||
        typeof payload.config_version !== "number" ||
        typeof payload.runtime_epoch !== "string" ||
        typeof payload.runtime_revision !== "number" ||
        typeof payload.state !== "string" ||
        typeof payload.active_instances !== "number" ||
        typeof payload.consumer_lag !== "number" ||
        typeof payload.error_code !== "string" ||
        typeof payload.error_message !== "string" ||
        typeof payload.observed_at !== "string" ||
        typeof payload.expires_at !== "string"
      ) return;
      const configVersion = payload.config_version;
      const runtimeEpoch = payload.runtime_epoch;
      const runtimeRevision = payload.runtime_revision;
      const runtimeState = payload.state as MailRuntimeState;
      const activeInstances = payload.active_instances;
      const consumerLag = payload.consumer_lag;
      const errorCode = payload.error_code;
      const errorMessage = payload.error_message;
      const observedAt = payload.observed_at;
      const expiresAt = payload.expires_at;
      queryClient.setQueryData<MailConsumerRuntimeWatch>(runtimeWatchKey, (current) => {
        if (!current || current.consumer_id !== detailConsumerID) return current;
        const watchEpoch = current.watch_lease_id.split(":", 2)[1];
        if (watchEpoch !== runtimeEpoch || current.config_version !== configVersion) return current;
        // [COMMENT]: Realtime revision chỉ tiến lên trong cùng watch epoch; event reorder không
        // được rollback badge/lag mới hơn.
        if (current.runtime && current.runtime.runtime_revision >= runtimeRevision) return current;
        return {
          ...current,
          runtime: {
            runtime_epoch: runtimeEpoch,
            runtime_revision: runtimeRevision,
            state: runtimeState,
            active_instances: activeInstances,
            consumer_lag: consumerLag,
            error_code: errorCode,
            error_message: errorMessage,
            observed_at: observedAt,
            expires_at: expiresAt,
          },
        };
      });
    });
  }, [detailConsumerID, queryClient, runtimeWatchKey, subscribeToStream]);

  const save = useMutation({
    mutationFn: async () => {
      const input: ConsumerWrite = {
        source_type: form.source_type,
        name: form.name.trim(), broker_resource_id: form.broker_resource_id.trim(), topic: form.topic.trim(),
        consumer_group: form.consumer_group.trim(), template_id: form.template_id.trim(), sender_profile_id: form.sender_profile_id.trim(),
        template_version: form.template_version, sender_version: form.sender_version, parallelism: form.parallelism,
      };
      // [COMMENT]: Code là Console identity bất biến; UUID vẫn là runtime identity do backend sinh.
      return editing
        ? updateMailConsumer(editing.id, { ...input, desired_state: editing.desired_state, expected_config_version: editing.config_version })
        : createMailConsumer({ ...input, code: form.code });
    },
    onSuccess: async (result) => {
      // [COMMENT]: Header activity là local-only; cùng operation_id sẽ được realtime result ghi đè.
      window.dispatchEvent(new CustomEvent("local-notification:add", {
        detail: {
          id: result.operation_id, title: editing ? "Updating mail consumer" : "Creating mail consumer",
          message: `${result.name} is being applied in the selected zone.`, type: "processing",
        }
      }));
      await queryClient.invalidateQueries({ queryKey });
      toast.success(editing ? "Consumer update scheduled" : "Consumer creation scheduled");
      setEditing(null); setForm(emptyForm); setFormOpen(false);
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const stateChange = useMutation({
    mutationFn: ({ consumer, action }: { consumer: MailConsumer; action: "pause" | "resume" }) => changeMailConsumerState(consumer.id, action, consumer.config_version),
    onSuccess: async (result) => {
      window.dispatchEvent(new CustomEvent("local-notification:add", {
        detail: {
          id: result.operation_id, title: "Applying mail consumer state",
          message: `${result.name} is being applied in the selected zone.`, type: "processing",
        }
      }));
      await queryClient.invalidateQueries({ queryKey }); toast.success("Consumer state change scheduled");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });
  const remove = useMutation({
    mutationFn: (consumer: MailConsumer) => deleteMailConsumer(consumer.id, consumer.config_version),
    onSuccess: (result, consumer) => {
      window.dispatchEvent(new CustomEvent("local-notification:add", {
        detail: {
          id: result.operation_id, title: "Deleting mail consumer",
          message: `${consumer.name} is being deleted from the selected zone.`, type: "processing",
        }
      }));
      toast.success("Consumer deletion scheduled");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  function openCreate() {
    setEditing(null); setForm(emptyForm); setFormOpen(true);
  }
  function openEdit(consumer: MailConsumer) {
    setEditing(consumer);
    setForm({
      code: consumer.code, name: consumer.name, source_type: consumer.source_type, broker_resource_id: consumer.broker_resource_id,
      topic: consumer.topic, consumer_group: consumer.consumer_group, template_id: consumer.template_id,
      template_version: consumer.template_version, sender_profile_id: consumer.sender_profile_id,
      sender_version: consumer.sender_version, parallelism: consumer.parallelism,
    });
    setFormOpen(true);
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    if (!/^[a-z][a-z0-9]*(-[a-z0-9]+)*$/.test(form.code) || !form.name.trim() || !form.broker_resource_id.trim() || !form.topic.trim() || !form.consumer_group.trim() ||
      !form.template_id.trim() || !form.sender_profile_id.trim()) {
      toast.error("Complete all required consumer fields."); return;
    }
    save.mutate();
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search name, source, consumer or template…" className="max-w-md" />
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => consumers.refetch()} disabled={consumers.isFetching}><RefreshCw className={consumers.isFetching ? "animate-spin" : ""} />Refresh</Button>
          {canCreate && <Button onClick={openCreate}><Plus />Create consumer</Button>}
        </div>
      </div>

      <Dialog open={Boolean(detailConsumerID)} onOpenChange={(open) => {
        if (!open && detailConsumerID) {
          // [COMMENT]: Xóa soft-state cache local khi đóng để lần mở sau không flash snapshot epoch cũ.
          queryClient.removeQueries({ queryKey: ["mail", scopeKey, "consumer-runtime-watch", detailConsumerID] });
          setDetailConsumerID(null);
        }
      }}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{consumerDetail.data?.name ?? "Consumer detail"}</DialogTitle>
            <DialogDescription>Desired configuration and the latest fresh runtime aggregate for this consumer.</DialogDescription>
          </DialogHeader>
          {consumerDetail.isLoading ? (
            <div className="flex items-center justify-center gap-2 py-10 text-muted-foreground"><Loader2 className="animate-spin" />Loading current state…</div>
          ) : consumerDetail.isError ? (
            <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-destructive">{errorMessage(consumerDetail.error)}</div>
          ) : consumerDetail.data ? (
            <div className="space-y-4">
              <div className="grid gap-3 rounded-lg border p-4 sm:grid-cols-2">
                <div><div className="text-xs text-muted-foreground">Desired state</div><Badge variant="outline" className="mt-1">{consumerDetail.data.desired_state}</Badge></div>
                <div><div className="text-xs text-muted-foreground">Config version</div><div className="mt-1 font-mono">v{consumerDetail.data.config_version}</div></div>
                <div><div className="text-xs text-muted-foreground">Source</div><div className="mt-1">{sourceLabels[consumerDetail.data.source_type].name} · <span className="font-mono">{consumerDetail.data.topic}</span></div></div>
                <div><div className="text-xs text-muted-foreground">Template</div><div className="mt-1 font-mono">{consumerDetail.data.template_id} · v{consumerDetail.data.template_version}</div></div>
              </div>
              {runtimeWatch.data?.runtime ? (
                <div className="grid gap-3 rounded-lg border p-4 sm:grid-cols-2">
                  <div><div className="text-xs text-muted-foreground">Reported runtime</div><Badge variant="outline" className="mt-1">{runtimeWatch.data.runtime.state}</Badge></div>
                  <div><div className="text-xs text-muted-foreground">Active logical slots</div><div className="mt-1 font-mono">{runtimeWatch.data.runtime.active_instances} / {consumerDetail.data.parallelism}</div></div>
                  <div><div className="text-xs text-muted-foreground">Reported config</div><div className="mt-1 font-mono">v{runtimeWatch.data.config_version}</div></div>
                  <div><div className="text-xs text-muted-foreground">Last report</div><div className="mt-1">{new Date(runtimeWatch.data.runtime.observed_at).toLocaleString()}</div></div>
                  {(runtimeWatch.data.runtime.error_code || runtimeWatch.data.runtime.error_message) && <div className="sm:col-span-2 rounded-md bg-destructive/5 p-3 text-sm text-destructive"><div className="font-mono">{runtimeWatch.data.runtime.error_code}</div>{runtimeWatch.data.runtime.error_message && <div className="mt-1">{runtimeWatch.data.runtime.error_message}</div>}</div>}
                </div>
              ) : runtimeWatch.isError ? (
                <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">Runtime watch is temporarily unavailable. Business configuration remains available.</div>
              ) : (
                <div className="flex items-center gap-2 rounded-lg border border-dashed p-5 text-sm text-muted-foreground">{runtimeWatch.isFetching && <Loader2 className="size-4 animate-spin" />}Waiting for a fresh runtime snapshot. This is distinct from a confirmed stopped state.</div>
              )}
            </div>
          ) : null}
        </DialogContent>
      </Dialog>

      {formOpen && (
        <form onSubmit={submit} className="space-y-5 rounded-xl border bg-card p-5">
          <div className="flex items-center justify-between"><div><h2 className="font-semibold">{editing ? "Edit consumer" : "Create consumer"}</h2><p className="text-xs text-muted-foreground">New consumers start paused. Broker credentials remain encrypted.</p></div><Button type="button" variant="ghost" size="icon" onClick={() => setFormOpen(false)}><X /></Button></div>
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            <div><Label htmlFor="consumer-name">Name</Label><Input id="consumer-name" value={form.name} maxLength={255} onChange={(e) => { const name = e.target.value; setForm({ ...form, name, code: editing ? form.code : name.toLowerCase().normalize("NFD").replace(/[\u0300-\u036f]/g, "").replace(/đ/g, "d").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63) }); }} required /></div>
            <div><Label htmlFor="consumer-code">Code</Label><Input id="consumer-code" value={form.code} maxLength={63} disabled={Boolean(editing)} onChange={(e) => setForm({ ...form, code: e.target.value.toLowerCase() })} required /><p className="mt-1 text-xs text-muted-foreground">Immutable; reusable after deletion.</p></div>
            <div><Label htmlFor="source-type">Stream type</Label><select id="source-type" value={form.source_type} disabled={Boolean(editing)} onChange={(e) => setForm({ ...form, source_type: e.target.value as MailSourceType })} className="mt-1 h-8 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50" required><option value="kafka">Kafka</option><option value="redis_stream">Redis Stream</option><option value="nats_jetstream">NATS JetStream</option><option value="rabbitmq">RabbitMQ</option></select></div>
            <div><Label htmlFor="broker-id">Broker resource ID</Label><Input id="broker-id" value={form.broker_resource_id} disabled={Boolean(editing)} onChange={(e) => setForm({ ...form, broker_resource_id: e.target.value })} required /><p className="mt-1 text-xs text-muted-foreground">Changing broker identity requires a freshly encrypted connection envelope.</p></div>
            <div><Label htmlFor="topic">{sourceLabels[form.source_type].source}</Label><Input id="topic" value={form.topic} onChange={(e) => setForm({ ...form, topic: e.target.value })} required /></div>
            <div><Label htmlFor="group">{sourceLabels[form.source_type].consumer}</Label><Input id="group" value={form.consumer_group} onChange={(e) => setForm({ ...form, consumer_group: e.target.value })} required /></div>
            <div><Label htmlFor="template-id">Template ID</Label><Input id="template-id" value={form.template_id} onChange={(e) => setForm({ ...form, template_id: e.target.value })} required /></div>
            <div><Label htmlFor="template-version">Template version</Label><Input id="template-version" type="number" min={1} value={form.template_version} onChange={(e) => setForm({ ...form, template_version: Number(e.target.value) })} required /></div>
            <div><Label htmlFor="sender-id">Sender profile ID</Label><Input id="sender-id" value={form.sender_profile_id} onChange={(e) => setForm({ ...form, sender_profile_id: e.target.value })} required /></div>
            <div><Label htmlFor="sender-version">Sender version</Label><Input id="sender-version" type="number" min={1} value={form.sender_version} onChange={(e) => setForm({ ...form, sender_version: Number(e.target.value) })} required /></div>
            <div><Label htmlFor="parallelism">Parallelism</Label><Input id="parallelism" type="number" min={1} max={256} value={form.parallelism} onChange={(e) => setForm({ ...form, parallelism: Number(e.target.value) })} required /></div>
            <div className="md:col-span-2 lg:col-span-3"><Label>Broker message contract</Label><pre className="mt-1 overflow-x-auto rounded-md border bg-muted/40 p-3 text-xs">{`{"to":"alice@example.com","parameter":{"name":"Alice","amount":123}}`}</pre><p className="mt-1 text-xs text-muted-foreground">Every message uses this fixed shape. Parameter keys must match the selected template placeholders.</p></div>
          </div>
          <div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={() => setFormOpen(false)}>Cancel</Button><Button disabled={save.isPending}>{save.isPending && <Loader2 className="animate-spin" />}{editing ? "Save configuration" : "Create paused"}</Button></div>
        </form>
      )}

      <div className="overflow-hidden rounded-xl border bg-card">
        {consumers.isLoading ? <div className="flex items-center justify-center gap-2 p-12 text-sm text-muted-foreground"><Loader2 className="animate-spin" />Loading consumers…</div> : consumers.isError ? <div className="p-10 text-center text-sm text-destructive">{errorMessage(consumers.error)}</div> : visible.length === 0 ? <div className="p-12 text-center text-sm text-muted-foreground">No consumers in this workspace.</div> : (
          <div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead className="border-b bg-muted/30 text-xs text-muted-foreground"><tr><th className="px-4 py-3">Consumer</th><th className="px-4 py-3">Stream source</th><th className="px-4 py-3">Template / sender</th><th className="px-4 py-3">Desired state</th><th className="px-4 py-3">Version</th><th className="px-4 py-3 text-right">Actions</th></tr></thead><tbody className="divide-y">
            {visible.map((consumer) => <tr key={consumer.id} className="hover:bg-muted/20"><td className="px-4 py-3"><div className="font-medium">{consumer.name}</div><div className="font-mono text-[11px] text-muted-foreground">{consumer.id}</div></td><td className="px-4 py-3"><Badge variant="outline" className="mb-1">{sourceLabels[consumer.source_type].name}</Badge>{!consumer.source_configured && <Badge variant="outline" className="mb-1 ml-1 text-amber-600">Needs credentials</Badge>}<div className="font-mono text-xs">{consumer.topic}</div><div className="text-xs text-muted-foreground">{consumer.consumer_group}</div></td><td className="px-4 py-3 text-xs"><div>{consumer.template_id} · v{consumer.template_version}</div><div className="text-muted-foreground">{consumer.sender_profile_id} · v{consumer.sender_version}</div></td><td className="px-4 py-3"><Badge variant="outline">{consumer.desired_state}</Badge></td><td className="px-4 py-3 font-mono text-xs">v{consumer.config_version}</td><td className="px-4 py-3"><div className="flex justify-end gap-1">
              <Button variant="ghost" size="icon-sm" title="View runtime detail" onClick={() => setDetailConsumerID(consumer.id)}><Eye /></Button>
              {canUpdate && <Button variant="ghost" size="icon-sm" title="Edit" onClick={() => openEdit(consumer)}><Pencil /></Button>}
              {canUpdate && <Button variant="ghost" size="icon-sm" title={consumer.desired_state === "enabled" ? "Pause" : consumer.source_configured ? "Resume" : "Configure broker credentials before resume"} disabled={stateChange.isPending || (consumer.desired_state !== "enabled" && !consumer.source_configured)} onClick={() => stateChange.mutate({ consumer, action: consumer.desired_state === "enabled" ? "pause" : "resume" })}>{consumer.desired_state === "enabled" ? <Pause /> : <Play />}</Button>}
              {canDelete && <AlertDialog><AlertDialogTrigger render={<Button variant="ghost" size="icon-sm" className="text-destructive" title="Delete" />}><Trash2 /></AlertDialogTrigger><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>Delete {consumer.name}?</AlertDialogTitle><AlertDialogDescription>An enabled consumer will drain before deletion. In-flight messages may finish.</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>Cancel</AlertDialogCancel><AlertDialogAction variant="destructive" onClick={() => remove.mutate(consumer)}>Request deletion</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>}
            </div></td></tr>)}
          </tbody></table></div>
        )}
      </div>
    </div>
  );
}
