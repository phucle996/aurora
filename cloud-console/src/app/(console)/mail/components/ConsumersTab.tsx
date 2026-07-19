"use client";

import { FormEvent, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, Pause, Pencil, Play, Plus, RefreshCw, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { type APIError } from "@/lib/api/fetcher";
import { changeMailConsumerState, createMailConsumer, deleteMailConsumer, listMailConsumers, type ConsumerWrite, type MailConsumer, updateMailConsumer } from "@/lib/api/mail";

type ConsumersTabProps = { enabled: boolean; scopeKey: string; canCreate: boolean; canUpdate: boolean; canDelete: boolean };
type ConsumerForm = ConsumerWrite & { code: string; variables: string };

const emptyForm: ConsumerForm = {
  code: "", name: "", source_type: "kafka", broker_resource_id: "", topic: "", consumer_group: "",
  mapping: { recipient_json_path: "$.recipient", external_message_id_json_path: "", variable_json_paths: {} },
  variables: "{}", template_id: "", template_version: 1, sender_profile_id: "", sender_version: 1, parallelism: 1,
};

function errorMessage(error: unknown): string {
  const apiError = error as APIError;
  if (apiError?.status === 409) return "Configuration changed in another session. Reload the latest version before retrying.";
  return apiError?.message || (error instanceof Error ? error.message : "Request failed");
}

export function ConsumersTab({ enabled, scopeKey, canCreate, canUpdate, canDelete }: ConsumersTabProps) {
  const queryClient = useQueryClient();
  const queryKey = ["mail", scopeKey, "consumers"] as const;
  const [search, setSearch] = useState("");
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<MailConsumer | null>(null);
  const [form, setForm] = useState<ConsumerForm>(emptyForm);

  const consumers = useQuery({
    queryKey,
    queryFn: ({ signal }) => listMailConsumers(signal),
    enabled,
  });
  const visible = useMemo(() => {
    const needle = search.trim().toLowerCase();
    if (!needle) return consumers.data ?? [];
    return (consumers.data ?? []).filter((item) => [item.name, item.topic, item.consumer_group, item.template_id].some((value) => value.toLowerCase().includes(needle)));
  }, [consumers.data, search]);

  const save = useMutation({
    mutationFn: async () => {
      let variablePaths: Record<string, string>;
      try { variablePaths = JSON.parse(form.variables) as Record<string, string>; } catch { throw new Error("Variable mappings must be a JSON object."); }
      const input: ConsumerWrite = {
		source_type: form.source_type,
        name: form.name.trim(), broker_resource_id: form.broker_resource_id.trim(), topic: form.topic.trim(),
        consumer_group: form.consumer_group.trim(), template_id: form.template_id.trim(), sender_profile_id: form.sender_profile_id.trim(),
		template_version: form.template_version, sender_version: form.sender_version, parallelism: form.parallelism,
        mapping: {
          recipient_json_path: form.mapping.recipient_json_path.trim(),
          external_message_id_json_path: form.mapping.external_message_id_json_path.trim(),
          variable_json_paths: variablePaths,
        },
      };
      // [COMMENT]: Code là Console identity bất biến; UUID vẫn là runtime identity do backend sinh.
      return editing
        ? updateMailConsumer(editing.id, { ...input, desired_state: editing.desired_state, expected_config_version: editing.config_version })
        : createMailConsumer({ ...input, code: form.code });
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey });
      toast.success(editing ? "Consumer updated" : "Consumer created paused");
      setEditing(null); setForm(emptyForm); setFormOpen(false);
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const stateChange = useMutation({
    mutationFn: ({ consumer, action }: { consumer: MailConsumer; action: "pause" | "resume" }) => changeMailConsumerState(consumer.id, action, consumer.config_version),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey }); toast.success("Consumer desired state updated"); },
    onError: (error) => toast.error(errorMessage(error)),
  });
  const remove = useMutation({
    mutationFn: (consumer: MailConsumer) => deleteMailConsumer(consumer.id, consumer.config_version),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey }); toast.success("Consumer deletion requested"); },
    onError: (error) => toast.error(errorMessage(error)),
  });

  function openCreate() {
    setEditing(null); setForm(emptyForm); setFormOpen(true);
  }
  function openEdit(consumer: MailConsumer) {
    setEditing(consumer);
    setForm({
      code: consumer.code, name: consumer.name, source_type: "kafka", broker_resource_id: consumer.broker_resource_id,
      topic: consumer.topic, consumer_group: consumer.consumer_group, mapping: consumer.mapping,
      variables: JSON.stringify(consumer.mapping.variable_json_paths ?? {}, null, 2), template_id: consumer.template_id,
      template_version: consumer.template_version, sender_profile_id: consumer.sender_profile_id,
      sender_version: consumer.sender_version, parallelism: consumer.parallelism,
    });
    setFormOpen(true);
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    if (!/^[a-z][a-z0-9]*(-[a-z0-9]+)*$/.test(form.code) || !form.name.trim() || !form.broker_resource_id.trim() || !form.topic.trim() || !form.consumer_group.trim() ||
      !form.mapping.recipient_json_path.trim().startsWith("$") || !form.template_id.trim() || !form.sender_profile_id.trim()) {
      toast.error("Complete all required fields and use a valid recipient JSONPath."); return;
    }
    save.mutate();
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search name, topic, group or template…" className="max-w-md" />
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => consumers.refetch()} disabled={consumers.isFetching}><RefreshCw className={consumers.isFetching ? "animate-spin" : ""} />Refresh</Button>
          {canCreate && <Button onClick={openCreate}><Plus />Create consumer</Button>}
        </div>
      </div>

      {formOpen && (
        <form onSubmit={submit} className="space-y-5 rounded-xl border bg-card p-5">
          <div className="flex items-center justify-between"><div><h2 className="font-semibold">{editing ? "Edit consumer" : "Create consumer"}</h2><p className="text-xs text-muted-foreground">New consumers start paused. Credentials remain in Vault.</p></div><Button type="button" variant="ghost" size="icon" onClick={() => setFormOpen(false)}><X /></Button></div>
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            <div><Label htmlFor="consumer-name">Name</Label><Input id="consumer-name" value={form.name} maxLength={255} onChange={(e) => { const name = e.target.value; setForm({ ...form, name, code: editing ? form.code : name.toLowerCase().normalize("NFD").replace(/[\u0300-\u036f]/g, "").replace(/đ/g, "d").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63) }); }} required /></div>
            <div><Label htmlFor="consumer-code">Code</Label><Input id="consumer-code" value={form.code} maxLength={63} disabled={Boolean(editing)} onChange={(e) => setForm({ ...form, code: e.target.value.toLowerCase() })} required /><p className="mt-1 text-xs text-muted-foreground">Immutable; reusable after deletion.</p></div>
            <div><Label htmlFor="broker-id">Broker resource ID</Label><Input id="broker-id" value={form.broker_resource_id} onChange={(e) => setForm({ ...form, broker_resource_id: e.target.value })} required /></div>
            <div><Label htmlFor="topic">Kafka topic</Label><Input id="topic" value={form.topic} onChange={(e) => setForm({ ...form, topic: e.target.value })} required /></div>
            <div><Label htmlFor="group">Consumer group</Label><Input id="group" value={form.consumer_group} onChange={(e) => setForm({ ...form, consumer_group: e.target.value })} required /></div>
            <div><Label htmlFor="recipient-path">Recipient JSONPath</Label><Input id="recipient-path" value={form.mapping.recipient_json_path} onChange={(e) => setForm({ ...form, mapping: { ...form.mapping, recipient_json_path: e.target.value } })} required /></div>
            <div><Label htmlFor="external-path">External message ID JSONPath</Label><Input id="external-path" value={form.mapping.external_message_id_json_path} onChange={(e) => setForm({ ...form, mapping: { ...form.mapping, external_message_id_json_path: e.target.value } })} /></div>
            <div><Label htmlFor="template-id">Template ID</Label><Input id="template-id" value={form.template_id} onChange={(e) => setForm({ ...form, template_id: e.target.value })} required /></div>
            <div><Label htmlFor="template-version">Template version</Label><Input id="template-version" type="number" min={1} value={form.template_version} onChange={(e) => setForm({ ...form, template_version: Number(e.target.value) })} required /></div>
            <div><Label htmlFor="sender-id">Sender profile ID</Label><Input id="sender-id" value={form.sender_profile_id} onChange={(e) => setForm({ ...form, sender_profile_id: e.target.value })} required /></div>
            <div><Label htmlFor="sender-version">Sender version</Label><Input id="sender-version" type="number" min={1} value={form.sender_version} onChange={(e) => setForm({ ...form, sender_version: Number(e.target.value) })} required /></div>
            <div><Label htmlFor="parallelism">Parallelism</Label><Input id="parallelism" type="number" min={1} max={256} value={form.parallelism} onChange={(e) => setForm({ ...form, parallelism: Number(e.target.value) })} required /></div>
            <div className="md:col-span-2"><Label htmlFor="variables">Variable paths (JSON object)</Label><Textarea id="variables" value={form.variables} onChange={(e) => setForm({ ...form, variables: e.target.value })} className="min-h-24 font-mono text-xs" /></div>
          </div>
          <div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={() => setFormOpen(false)}>Cancel</Button><Button disabled={save.isPending}>{save.isPending && <Loader2 className="animate-spin" />}{editing ? "Save configuration" : "Create paused"}</Button></div>
        </form>
      )}

      <div className="overflow-hidden rounded-xl border bg-card">
        {consumers.isLoading ? <div className="flex items-center justify-center gap-2 p-12 text-sm text-muted-foreground"><Loader2 className="animate-spin" />Loading consumers…</div> : consumers.isError ? <div className="p-10 text-center text-sm text-destructive">{errorMessage(consumers.error)}</div> : visible.length === 0 ? <div className="p-12 text-center text-sm text-muted-foreground">No consumers in this workspace.</div> : (
          <div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead className="border-b bg-muted/30 text-xs text-muted-foreground"><tr><th className="px-4 py-3">Consumer</th><th className="px-4 py-3">Kafka source</th><th className="px-4 py-3">Template / sender</th><th className="px-4 py-3">Desired state</th><th className="px-4 py-3">Version</th><th className="px-4 py-3 text-right">Actions</th></tr></thead><tbody className="divide-y">
            {visible.map((consumer) => <tr key={consumer.id} className="hover:bg-muted/20"><td className="px-4 py-3"><div className="font-medium">{consumer.name}</div><div className="font-mono text-[11px] text-muted-foreground">{consumer.id}</div></td><td className="px-4 py-3"><div className="font-mono text-xs">{consumer.topic}</div><div className="text-xs text-muted-foreground">{consumer.consumer_group}</div></td><td className="px-4 py-3 text-xs"><div>{consumer.template_id} · v{consumer.template_version}</div><div className="text-muted-foreground">{consumer.sender_profile_id} · v{consumer.sender_version}</div></td><td className="px-4 py-3"><Badge variant="outline">{consumer.desired_state}</Badge></td><td className="px-4 py-3 font-mono text-xs">v{consumer.config_version}</td><td className="px-4 py-3"><div className="flex justify-end gap-1">
              {canUpdate && <Button variant="ghost" size="icon-sm" title="Edit" onClick={() => openEdit(consumer)}><Pencil /></Button>}
              {canUpdate && consumer.desired_state !== "deleting" && <Button variant="ghost" size="icon-sm" title={consumer.desired_state === "enabled" ? "Pause" : "Resume"} disabled={stateChange.isPending} onClick={() => stateChange.mutate({ consumer, action: consumer.desired_state === "enabled" ? "pause" : "resume" })}>{consumer.desired_state === "enabled" ? <Pause /> : <Play />}</Button>}
              {canDelete && <AlertDialog><AlertDialogTrigger render={<Button variant="ghost" size="icon-sm" className="text-destructive" title="Delete" />}><Trash2 /></AlertDialogTrigger><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>Delete {consumer.name}?</AlertDialogTitle><AlertDialogDescription>An enabled consumer will drain before deletion. In-flight messages may finish.</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>Cancel</AlertDialogCancel><AlertDialogAction variant="destructive" onClick={() => remove.mutate(consumer)}>Request deletion</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>}
            </div></td></tr>)}
          </tbody></table></div>
        )}
      </div>
    </div>
  );
}
