"use client";

import { FormEvent, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, FilePlus2, Loader2, Plus, RefreshCw, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { type APIError } from "@/lib/api/fetcher";
import { createMailTemplate, deleteMailTemplate, getMailTemplate, listMailTemplates, listMailTemplateVersions, publishMailTemplate, type MailTemplate, type TemplateContentWrite } from "@/lib/api/mail";
import { useRealtime } from "@/context/RealtimeContext";

type TemplatesTabProps = { enabled: boolean; scopeKey: string; canCreate: boolean; canUpdate: boolean; canDelete: boolean };
type TemplateForm = TemplateContentWrite & { code: string; name: string };
type MailTemplateJobNotification = { operation?: unknown; resource_id?: string; status?: string };
const emptyForm: TemplateForm = { code: "", name: "", subject_template: "", html_template: "" };

function errorMessage(error: unknown): string {
  const apiError = error as APIError;
  if (apiError?.status === 409) return "Template changed in another session. Reload the latest revision before retrying.";
  return apiError?.message || (error instanceof Error ? error.message : "Request failed");
}
export function TemplatesTab({ enabled, scopeKey, canCreate, canUpdate, canDelete }: TemplatesTabProps) {
  const queryClient = useQueryClient();
  const { subscribeToStream } = useRealtime();
  const listKey = ["mail", scopeKey, "templates"] as const;
  const [search, setSearch] = useState("");
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [formMode, setFormMode] = useState<"create" | "publish" | null>(null);
  const [form, setForm] = useState<TemplateForm>(emptyForm);

  const templates = useQuery({ queryKey: listKey, queryFn: ({ signal }) => listMailTemplates(signal), enabled });
  const detail = useQuery({ queryKey: ["mail", scopeKey, "template", selectedID], queryFn: ({ signal }) => getMailTemplate(selectedID!, signal), enabled: Boolean(selectedID) });
  const versions = useQuery({ queryKey: ["mail", scopeKey, "template", selectedID, "versions"], queryFn: ({ signal }) => listMailTemplateVersions(selectedID!, signal), enabled: Boolean(selectedID) });
  const visible = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return (templates.data ?? []).filter((item) => !needle || item.name.toLowerCase().includes(needle) || item.id.toLowerCase().includes(needle));
  }, [search, templates.data]);

  useEffect(() => {
    // [COMMENT]: Không reset state thủ công tại đây — parent component đặt key={scopeKey} cho TemplatesTab
    // để React tự remount và reset toàn bộ state mỗi khi scope thay đổi. Cách này an toàn hơn, không có race condition.
    void 0;
  }, [scopeKey]);

  useEffect(() => {
    // [COMMENT]: Screen không poll operation; terminal Centrifugo signal merge lại list/detail/version read model.
    return subscribeToStream("notification", "job.notification", (payload: MailTemplateJobNotification) => {
      if (typeof payload?.operation !== "string" || !payload.operation.startsWith("mail.template.") || !payload.resource_id || typeof payload.status !== "string" || !["SUCCESS", "FAILED"].includes(payload.status)) return;
      void queryClient.invalidateQueries({ queryKey: ["mail", scopeKey, "templates"] });
      if (selectedID === payload.resource_id) {
        void queryClient.invalidateQueries({ queryKey: ["mail", scopeKey, "template", selectedID] });
        void queryClient.invalidateQueries({ queryKey: ["mail", scopeKey, "template", selectedID, "versions"] });
        if (payload.operation === "mail.template.deleted" && payload.status === "SUCCESS") setSelectedID(null);
      }
    });
  }, [queryClient, scopeKey, selectedID, subscribeToStream]);

  const save = useMutation({
    mutationFn: async () => {
      const content: TemplateContentWrite = {
        subject_template: form.subject_template.trim(), html_template: form.html_template,
      };
      if (formMode === "publish" && detail.data) return publishMailTemplate(detail.data.template.id, detail.data.template.template_revision, content);
      return createMailTemplate({ ...content, code: form.code, name: form.name.trim() });
    },
    onSuccess: async (result) => {
      window.dispatchEvent(new CustomEvent("local-notification:add", { detail: {
        id: result.operation_id, title: formMode === "publish" ? "Publishing mail template" : "Creating mail template",
        message: `${result.template.name} is being applied in the selected zone.`, type: "processing",
      } }));
      await queryClient.invalidateQueries({ queryKey: ["mail", scopeKey] });
      setSelectedID(result.template.id); setFormMode(null); setForm(emptyForm);
      toast.success(formMode === "publish" ? "Template publish scheduled" : "Template creation scheduled");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });
  const deleteMutation = useMutation({
    mutationFn: (template: MailTemplate) => deleteMailTemplate(template.id, template.template_revision),
    onSuccess: (result, template) => {
      window.dispatchEvent(new CustomEvent("local-notification:add", { detail: {
        id: result.operation_id, title: "Deleting mail template",
        message: `${template.name} is being deleted from the selected zone.`, type: "processing",
      } }));
      toast.success("Template deletion scheduled");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  function startCreate() {
    setSelectedID(null); setForm(emptyForm); setFormMode("create");
  }
  function startPublish() {
    if (!detail.data) return;
    const current = detail.data.current_version;
    setForm({ code: detail.data.template.code, name: detail.data.template.name, subject_template: current.subject_template, html_template: current.html_template });
    setFormMode("publish");
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    if ((formMode === "create" && (!form.name.trim() || !/^[a-z][a-z0-9]*(-[a-z0-9]+)*$/.test(form.code))) || !form.subject_template.trim() || !form.html_template.trim()) {
      toast.error("Name, subject and HTML body are required."); return;
    }
    save.mutate();
  }

  if (selectedID && !formMode) {
    const template = detail.data?.template;
    const current = detail.data?.current_version;
    return (
      <div className="space-y-4">
        <Button variant="ghost" onClick={() => setSelectedID(null)}>← Back to templates</Button>
        {detail.isLoading ? <div className="flex justify-center p-12"><Loader2 className="animate-spin" /></div> : detail.isError || !template || !current ? <div className="rounded-lg border p-8 text-destructive">{errorMessage(detail.error)}</div> : <>
          <div className="rounded-xl border bg-card p-5"><div className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between"><div><div className="flex items-center gap-2"><h2 className="text-lg font-semibold">{template.name}</h2></div><p className="mt-1 font-mono text-xs text-muted-foreground">{template.code}</p><p className="mt-3 text-sm">Current version <strong>v{template.current_version}</strong> · revision {template.template_revision}</p></div><div className="flex gap-2">{canUpdate && <Button onClick={startPublish}><FilePlus2 />Publish new version</Button>}{canDelete && <AlertDialog><AlertDialogTrigger render={<Button variant="outline" />}><Trash2 />Delete</AlertDialogTrigger><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>Delete this template permanently?</AlertDialogTitle><AlertDialogDescription>All immutable versions will be removed. Delete or move every active consumer using this template first.</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>Cancel</AlertDialogCancel><AlertDialogAction variant="destructive" onClick={() => deleteMutation.mutate(template)}>Delete template</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>}</div></div></div>
          <div className="grid gap-4 lg:grid-cols-2"><div className="rounded-xl border bg-card p-5"><h3 className="font-semibold">Current content</h3><div className="mt-4 space-y-4"><div><div className="text-xs font-medium text-muted-foreground">Subject</div><div className="mt-1 rounded-md bg-muted/40 p-3 text-sm">{current.subject_template}</div></div>{current.html_template && <div><div className="text-xs font-medium text-muted-foreground">HTML source</div><pre className="mt-1 max-h-64 overflow-auto whitespace-pre-wrap rounded-md bg-muted/40 p-3 text-xs">{current.html_template}</pre></div>}</div></div><div className="rounded-xl border bg-card p-5"><h3 className="font-semibold">Immutable versions</h3>{versions.isLoading ? <Loader2 className="mt-5 animate-spin" /> : <div className="mt-4 divide-y">{(versions.data ?? []).map((version) => <div key={version.version} className="flex items-center justify-between py-3 text-sm"><div><strong>v{version.version}</strong><div className="font-mono text-[11px] text-muted-foreground">{version.content_sha256.slice(0, 16)}…</div></div><time className="text-xs text-muted-foreground">{new Date(version.created_at).toLocaleString()}</time></div>)}</div>}</div></div>
        </>}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"><Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search template name or ID…" className="max-w-md" /><div className="flex gap-2"><Button variant="outline" onClick={() => templates.refetch()} disabled={templates.isFetching}><RefreshCw className={templates.isFetching ? "animate-spin" : ""} />Refresh</Button>{canCreate && <Button onClick={startCreate}><Plus />Create template</Button>}</div></div>

      {formMode && <form onSubmit={submit} className="space-y-5 rounded-xl border bg-card p-5"><div className="flex items-center justify-between"><div><h2 className="font-semibold">{formMode === "create" ? "Create template" : `Publish version ${detail.data ? detail.data.template.current_version + 1 : ""}`}</h2><p className="text-xs text-muted-foreground">Published versions are immutable. Dataplane discovers {"{{placeholders}}"} while rendering.</p></div><Button type="button" variant="ghost" size="icon" onClick={() => setFormMode(null)}><X /></Button></div><div className="grid gap-4 lg:grid-cols-2">{formMode === "create" && <><div><Label htmlFor="template-name">Name</Label><Input id="template-name" value={form.name} maxLength={255} onChange={(e) => { const name = e.target.value; setForm({ ...form, name, code: name.toLowerCase().normalize("NFD").replace(/[\u0300-\u036f]/g, "").replace(/đ/g, "d").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63) }); }} required /></div><div><Label htmlFor="template-code">Code</Label><Input id="template-code" value={form.code} maxLength={63} onChange={(e) => setForm({ ...form, code: e.target.value.toLowerCase() })} required /></div></>}<div className="lg:col-span-2"><Label htmlFor="subject">Subject</Label><Input id="subject" value={form.subject_template} maxLength={998} onChange={(e) => setForm({ ...form, subject_template: e.target.value })} required /></div><div><Label htmlFor="html-body">HTML body</Label><Textarea id="html-body" value={form.html_template} onChange={(e) => setForm({ ...form, html_template: e.target.value })} className="min-h-64 font-mono text-xs" /></div></div><div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={() => setFormMode(null)}>Cancel</Button><Button disabled={save.isPending}>{save.isPending && <Loader2 className="animate-spin" />}{formMode === "create" ? "Create template" : "Publish immutable version"}</Button></div></form>}

      <div className="overflow-hidden rounded-xl border bg-card">{templates.isLoading ? <div className="flex items-center justify-center gap-2 p-12 text-sm text-muted-foreground"><Loader2 className="animate-spin" />Loading templates…</div> : templates.isError ? <div className="p-10 text-center text-sm text-destructive">{errorMessage(templates.error)}</div> : visible.length === 0 ? <div className="p-12 text-center text-sm text-muted-foreground">No templates in this workspace.</div> : <div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead className="border-b bg-muted/30 text-xs text-muted-foreground"><tr><th className="px-4 py-3">Template</th><th className="px-4 py-3">Current version</th><th className="px-4 py-3">Revision</th><th className="px-4 py-3">Updated</th><th className="px-4 py-3 text-right">Action</th></tr></thead><tbody className="divide-y">{visible.map((template) => <tr key={template.id} className="hover:bg-muted/20"><td className="px-4 py-3"><div className="font-medium">{template.name}</div><div className="font-mono text-[11px] text-muted-foreground">{template.id}</div></td><td className="px-4 py-3 font-mono">v{template.current_version}</td><td className="px-4 py-3 font-mono">{template.template_revision}</td><td className="px-4 py-3 text-xs text-muted-foreground">{new Date(template.updated_at).toLocaleString()}</td><td className="px-4 py-3 text-right"><Button variant="ghost" size="sm" onClick={() => setSelectedID(template.id)}><Eye />View</Button></td></tr>)}</tbody></table></div>}</div>
    </div>
  );
}
