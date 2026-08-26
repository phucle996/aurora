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
import { type APIError } from "@/shared/api/http";
import { getMailEstimate } from "@/features/billing/api";
import { formatMicroUnits } from "@/features/billing/money";
import { changeMailConsumerState, createMailConsumer, deleteMailConsumer, getMailConsumer, listMailConsumers, mintMailConsumerRuntimeRead, type ConsumerWrite, type MailConsumer, type MailSourceType, updateMailConsumer } from "@/features/mail/api";
import { useRealtime } from "@/realtime/provider";
import { publicRuntimeConfig } from "@/runtime-config";

type ConsumersTabProps = { enabled: boolean; scopeKey: string; canCreate: boolean; canUpdate: boolean; canDelete: boolean };
type ConsumerForm = ConsumerWrite & { code: string };
type MailConsumerJobNotification = { operation?: unknown; resource_id?: string; status?: string };
type RuntimeHealth = { state: string; activeInstances: number; observedAt: string };

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
  const [runtimeHealth, setRuntimeHealth] = useState<RuntimeHealth | null>(null);
  const [runtimeStreamStatus, setRuntimeStreamStatus] = useState<"idle" | "connecting" | "open" | "error">("idle");

	const consumers = useQuery({
    queryKey,
    queryFn: ({ signal }) => listMailConsumers(signal),
    enabled,
	});
	const pricing = useQuery({
		queryKey: ["mail", scopeKey, "accepted-recipient-price", "1000"],
		queryFn: ({ signal }) => getMailEstimate("1000", signal),
		enabled,
		retry: false,
	});
	const acceptedRecipientPrice = pricing.data ? formatMicroUnits(pricing.data.estimate_micro_units, pricing.data.currency) : null;
  const consumerDetail = useQuery({
    queryKey: ["mail", scopeKey, "consumer-detail", detailConsumerID],
    queryFn: ({ signal }) => getMailConsumer(detailConsumerID as string, signal),
    enabled: enabled && Boolean(detailConsumerID),
  });
  const visible = useMemo(() => {
    const needle = search.trim().toLowerCase();
    if (!needle) return consumers.data ?? [];
    return (consumers.data ?? []).filter((item) => [item.name, item.topic, item.consumer_group, item.template_id].some((value) => value.toLowerCase().includes(needle)));
  }, [consumers.data, search]);

  useEffect(() => {
    // [COMMENT]: Không poll status URL và không lưu audit ở UI; terminal Centrifugo signal chỉ merge lại read model liên quan.
    return subscribeToStream("notification", "job.notification", (payload: MailConsumerJobNotification) => {
      if (typeof payload?.operation !== "string" || !payload.operation.startsWith("mail.consumer.") || !payload.resource_id || typeof payload.status !== "string" || !["SUCCESS", "FAILED"].includes(payload.status)) return;
      void queryClient.invalidateQueries({ queryKey: ["mail", scopeKey, "consumers"] });
      if (detailConsumerID === payload.resource_id) {
        void queryClient.invalidateQueries({ queryKey: ["mail", scopeKey, "consumer-detail", detailConsumerID] });
      }
    });
  }, [detailConsumerID, queryClient, scopeKey, subscribeToStream]);

  useEffect(() => {
    if (!enabled || !detailConsumerID) {
      return;
    }
    const controller = new AbortController();
    let latestSampleAt = 0;
    const freshness = window.setInterval(() => {
      if (latestSampleAt > 0 && Date.now() - latestSampleAt > 45_000) {
        latestSampleAt = 0;
        setRuntimeHealth(null);
      }
    }, 5_000);

    void (async () => {
      while (!controller.signal.aborted) {
        try {
          const ticket = await mintMailConsumerRuntimeRead(
            detailConsumerID,
            "health",
            60,
            controller.signal,
          );
          const baseDomain = publicRuntimeConfig()?.zonePublicBaseDomain ?? "";
          const ticketExpiresAt = Date.parse(ticket.expires_at);
          const expectedPath = `/zone-public/v1/runtime/mail/consumer/${detailConsumerID}/health?from_seconds=60`;
          if (
            !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(ticket.zone_code) ||
            baseDomain.length > 253 ||
            !baseDomain.split(".").every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label)) ||
            ticket.method !== "GET" ||
            ticket.path !== expectedPath ||
            !Number.isFinite(ticketExpiresAt) ||
            ticketExpiresAt <= Date.now()
          ) {
            throw new Error("Runtime assertion response is invalid");
          }
          const response = await fetch(`https://${ticket.zone_code}.${baseDomain}${ticket.path}`, {
            method: "GET",
            headers: {
              Accept: "text/event-stream",
              "X-Aurora-Runtime-Assertion": ticket.assertion,
              "X-Aurora-Runtime-Signature": ticket.signature,
              "X-Aurora-Runtime-Key-Id": ticket.key_id,
            },
            credentials: "omit",
            cache: "no-store",
            signal: controller.signal,
          });
          if (!response.ok || !response.body) throw new Error("Zone runtime stream is unavailable");
          setRuntimeStreamStatus("open");

          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";
          let eventType = "message";
          let dataLines: string[] = [];
          while (!controller.signal.aborted) {
            const chunk = await reader.read();
            buffer += decoder.decode(chunk.value ?? new Uint8Array(), { stream: !chunk.done });
            let boundary = buffer.indexOf("\n");
            while (boundary >= 0) {
              const line = buffer.slice(0, boundary).replace(/\r$/, "");
              buffer = buffer.slice(boundary + 1);
              if (line === "") {
                if ((eventType === "runtime.snapshot" || eventType === "runtime.metric") && dataLines.length > 0) {
                  const frame = JSON.parse(dataLines.join("\n")) as {
                    payload?: { data?: { data?: { result?: unknown } } };
                  };
                  const result = frame.payload?.data?.data?.result;
                  if (Array.isArray(result)) {
                    const states: number[] = [];
                    let newestSampleAt = 0;
                    for (const series of result) {
                      if (!series || typeof series !== "object") continue;
                      const values = (series as { values?: unknown }).values;
                      if (!Array.isArray(values) || values.length === 0) continue;
                      const latest = values[values.length - 1];
                      if (!Array.isArray(latest) || latest.length < 2) continue;
                      const sampleAt = Number(latest[0]) * 1_000;
                      const value = Number(latest[1]);
                      if (Number.isFinite(sampleAt)) newestSampleAt = Math.max(newestSampleAt, sampleAt);
                      if (Number.isInteger(value) && value >= 1 && value <= 7) states.push(value);
                    }
                    const priority = [6, 7, 5, 2, 3, 4, 1];
                    const selected = priority.find((state) => states.includes(state));
                    const labels: Record<number, string> = { 1: "stopped", 2: "starting", 3: "running", 4: "paused", 5: "draining", 6: "error", 7: "degraded" };
                    if (selected && newestSampleAt > 0 && Date.now() - newestSampleAt <= 45_000) {
                      latestSampleAt = newestSampleAt;
                      setRuntimeHealth({
                        state: labels[selected],
                        activeInstances: states.filter((state) => state !== 1).length,
                        observedAt: new Date(newestSampleAt).toISOString(),
                      });
                    } else {
                      latestSampleAt = 0;
                      setRuntimeHealth(null);
                    }
                  }
                } else if (eventType === "stream.error") {
                  throw new Error("Zone runtime stream expired");
                }
                eventType = "message";
                dataLines = [];
              } else if (line.startsWith("event:")) {
                eventType = line.slice(6).trim();
              } else if (line.startsWith("data:")) {
                dataLines.push(line.slice(5).trimStart());
              }
              boundary = buffer.indexOf("\n");
            }
            if (chunk.done) break;
          }
        } catch {
          if (controller.signal.aborted) return;
          setRuntimeStreamStatus("error");
        }
        await new Promise<void>((resolve) => {
          const abort = () => {
            window.clearTimeout(retry);
            resolve();
          };
          const retry = window.setTimeout(() => {
            controller.signal.removeEventListener("abort", abort);
            resolve();
          }, 1_000 + Math.floor(Math.random() * 500));
          controller.signal.addEventListener("abort", abort, { once: true });
        });
      }
    })();

    return () => {
      controller.abort();
      window.clearInterval(freshness);
    };
  }, [detailConsumerID, enabled]);

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
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey });
      toast.success(editing ? "Consumer update scheduled" : "Consumer creation scheduled");
      setEditing(null); setForm(emptyForm); setFormOpen(false);
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const stateChange = useMutation({
    mutationFn: ({ consumer, action }: { consumer: MailConsumer; action: "pause" | "resume" }) => changeMailConsumerState(consumer.id, action, consumer.config_version),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey }); toast.success("Consumer state change scheduled");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });
  const remove = useMutation({
    mutationFn: (consumer: MailConsumer) => deleteMailConsumer(consumer.id, consumer.config_version),
    onSuccess: () => {
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
			<div className="rounded-lg border bg-card px-4 py-3">
				<div className="text-sm font-medium">Accepted-recipient pricing</div>
				{acceptedRecipientPrice ? <div className="mt-1 text-lg font-semibold">{acceptedRecipientPrice} <span className="text-xs font-normal text-muted-foreground">per 1,000 successfully accepted recipients</span></div> : <div className="mt-1 text-xs text-amber-600">Pricing is not active. Consumers remain paused until Cost publishes Mail version 1.</div>}
				<div className="mt-1 text-xs text-muted-foreground">Rejected, retryable and ambiguous submissions are not charged.</div>
			</div>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search name, source, consumer or template…" className="max-w-md" />
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => consumers.refetch()} disabled={consumers.isFetching}><RefreshCw className={consumers.isFetching ? "animate-spin" : ""} />Refresh</Button>
          {canCreate && <Button onClick={openCreate}><Plus />Create consumer</Button>}
        </div>
      </div>

      <Dialog open={Boolean(detailConsumerID)} onOpenChange={(open) => {
        if (!open && detailConsumerID) {
          setRuntimeHealth(null);
          setRuntimeStreamStatus("idle");
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
              {runtimeHealth ? (
                <div className="grid gap-3 rounded-lg border p-4 sm:grid-cols-2">
                  <div><div className="text-xs text-muted-foreground">Zone runtime</div><Badge variant="outline" className="mt-1">{runtimeHealth.state}</Badge></div>
                  <div><div className="text-xs text-muted-foreground">Active logical slots</div><div className="mt-1 font-mono">{runtimeHealth.activeInstances} / {consumerDetail.data.parallelism}</div></div>
                  <div><div className="text-xs text-muted-foreground">Runtime source</div><div className="mt-1">Zone OTel · Victoria</div></div>
                  <div><div className="text-xs text-muted-foreground">Last sample</div><div className="mt-1">{new Date(runtimeHealth.observedAt).toLocaleString()}</div></div>
                </div>
              ) : runtimeStreamStatus === "error" ? (
                <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">Zone runtime stream is reconnecting. Business configuration remains available.</div>
              ) : (
                <div className="flex items-center gap-2 rounded-lg border border-dashed p-5 text-sm text-muted-foreground">{runtimeStreamStatus === "connecting" && <Loader2 className="size-4 animate-spin" />}Waiting for a fresh Zone runtime snapshot. This is distinct from a confirmed stopped state.</div>
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
              <Button variant="ghost" size="icon-sm" title="View runtime detail" onClick={() => {
                setRuntimeHealth(null);
                setRuntimeStreamStatus("connecting");
                setDetailConsumerID(consumer.id);
              }}><Eye /></Button>
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
