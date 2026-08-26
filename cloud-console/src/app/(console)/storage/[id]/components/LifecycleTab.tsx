"use client";

import React, { useState } from "react";
import {
  Clock,
  Plus,
  Trash2,
  Edit2,
  ShieldAlert,
  Layers,
  History,
  Check,
  X,
  Loader2,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  type BucketItem,
  type BucketLifecycleRule,
  updateBucketLifecycle,
  updateBucketVersioning,
} from "@/features/storage/api";

interface LifecycleTabProps {
  bucket: BucketItem;
  onRefresh: () => void;
}

export function LifecycleTab({ bucket, onRefresh }: LifecycleTabProps) {
  const rules = bucket.lifecycle_rules || [];
  const versioningEnabled = !!bucket.versioning_enabled;

  const [modalOpen, setModalOpen] = useState(false);
  const [editingRuleIndex, setEditingRuleIndex] = useState<number | null>(null);
  const [saving, setSaving] = useState(false);
  const [enablingVersioning, setEnablingVersioning] = useState(false);

  // Form state
  const [ruleId, setRuleId] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [prefix, setPrefix] = useState("");
  const [expirationDays, setExpirationDays] = useState<number>(30);
  const [enableExpiration, setEnableExpiration] = useState(true);
  const [noncurrentExpirationDays, setNoncurrentExpirationDays] = useState<number>(14);
  const [enableNoncurrentExpiration, setEnableNoncurrentExpiration] = useState(false);
  const [abortMultipartDays, setAbortMultipartDays] = useState<number>(7);
  const [enableAbortMultipart, setEnableAbortMultipart] = useState(true);

  const openCreateModal = () => {
    setEditingRuleIndex(null);
    setRuleId(`rule-${Date.now().toString().slice(-4)}`);
    setEnabled(true);
    setPrefix("");
    setExpirationDays(30);
    setEnableExpiration(true);
    setNoncurrentExpirationDays(14);
    setEnableNoncurrentExpiration(false);
    setAbortMultipartDays(7);
    setEnableAbortMultipart(true);
    setModalOpen(true);
  };

  const openEditModal = (rule: BucketLifecycleRule, index: number) => {
    setEditingRuleIndex(index);
    setRuleId(rule.id);
    setEnabled(rule.enabled);
    setPrefix(rule.prefix);
    setExpirationDays(rule.expiration_days || 30);
    setEnableExpiration(rule.expiration_days > 0);
    setNoncurrentExpirationDays(rule.noncurrent_version_expiration_days || 14);
    setEnableNoncurrentExpiration(rule.noncurrent_version_expiration_days > 0);
    setAbortMultipartDays(rule.abort_incomplete_multipart_upload_days || 7);
    setEnableAbortMultipart(rule.abort_incomplete_multipart_upload_days > 0);
    setModalOpen(true);
  };

  const handleQuickEnableVersioning = async () => {
    try {
      setEnablingVersioning(true);
      await updateBucketVersioning(bucket.id, true);
      toast.success("Bucket Versioning has been enabled successfully.");
      onRefresh();
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : "Failed to enable versioning");
    } finally {
      setEnablingVersioning(false);
    }
  };

  const handleToggleRule = async (index: number) => {
    const updated = [...rules];
    updated[index] = { ...updated[index], enabled: !updated[index].enabled };
    try {
      await updateBucketLifecycle(bucket.id, updated);
      toast.success(`Rule "${updated[index].id}" ${updated[index].enabled ? "enabled" : "disabled"}`);
      onRefresh();
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : "Failed to toggle rule");
    }
  };

  const handleDeleteRule = async (index: number) => {
    const ruleToDelete = rules[index];
    const updated = rules.filter((_, i) => i !== index);
    try {
      await updateBucketLifecycle(bucket.id, updated);
      toast.success(`Rule "${ruleToDelete.id}" removed`);
      onRefresh();
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : "Failed to delete rule");
    }
  };

  const handleSaveRule = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanId = ruleId.trim();
    if (!cleanId) {
      toast.error("Rule ID is required.");
      return;
    }

    const effectiveNoncurrentDays = enableNoncurrentExpiration ? Math.max(0, noncurrentExpirationDays) : 0;
    if (effectiveNoncurrentDays > 0 && !versioningEnabled) {
      toast.error("Noncurrent version expiration requires Bucket Versioning to be enabled first.");
      return;
    }

    const newRule: BucketLifecycleRule = {
      id: cleanId,
      enabled,
      prefix: prefix.trim(),
      expiration_days: enableExpiration ? Math.max(0, expirationDays) : 0,
      noncurrent_version_expiration_days: effectiveNoncurrentDays,
      abort_incomplete_multipart_upload_days: enableAbortMultipart ? Math.max(0, abortMultipartDays) : 0,
    };

    let updatedRules: BucketLifecycleRule[];
    if (editingRuleIndex !== null) {
      updatedRules = [...rules];
      updatedRules[editingRuleIndex] = newRule;
    } else {
      if (rules.some((r) => r.id === cleanId)) {
        toast.error(`A rule with ID "${cleanId}" already exists.`);
        return;
      }
      updatedRules = [...rules, newRule];
    }

    try {
      setSaving(true);
      await updateBucketLifecycle(bucket.id, updatedRules);
      toast.success(editingRuleIndex !== null ? "Lifecycle rule updated." : "Lifecycle rule created.");
      setModalOpen(false);
      onRefresh();
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : "Failed to save lifecycle rule");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-lg font-semibold tracking-tight text-foreground flex items-center gap-2">
            <Clock className="h-5 w-5 text-primary" />
            Object Lifecycle Management
          </h2>
          <p className="text-sm text-muted-foreground">
            Automatically expire objects, clean up noncurrent versions, and abort incomplete multipart uploads.
          </p>
        </div>
        <Button onClick={openCreateModal} className="shrink-0 gap-2">
          <Plus className="h-4 w-4" />
          Add Lifecycle Rule
        </Button>
      </div>

      {/* Versioning status alert */}
      {!versioningEnabled && (
        <div className="flex items-start gap-4 rounded-xl border border-amber-500/20 bg-amber-500/5 p-4 text-sm text-amber-500">
          <ShieldAlert className="h-5 w-5 shrink-0 mt-0.5" />
          <div className="flex-1 space-y-1">
            <p className="font-medium text-foreground">Bucket Versioning is Disabled</p>
            <p className="text-xs text-muted-foreground leading-relaxed">
              Rules targeting noncurrent versions (previous revisions of modified or deleted objects) require Versioning to be enabled on this bucket.
            </p>
          </div>
          <Button
            size="sm"
            variant="outline"
            disabled={enablingVersioning}
            onClick={handleQuickEnableVersioning}
            className="shrink-0 gap-2 border-amber-500/30 text-amber-500 hover:bg-amber-500/10"
          >
            {enablingVersioning ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <History className="h-3.5 w-3.5" />}
            Enable Versioning
          </Button>
        </div>
      )}

      {/* Rules list */}
      {rules.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-border/80 p-12 text-center">
          <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-muted text-muted-foreground mb-4">
            <Clock className="h-6 w-6" />
          </div>
          <h3 className="text-base font-semibold text-foreground">No lifecycle rules configured</h3>
          <p className="mt-1 max-w-sm text-sm text-muted-foreground">
            Configure lifecycle rules to manage storage costs and automatically clean up expired files or abandoned multipart uploads.
          </p>
          <Button onClick={openCreateModal} variant="outline" className="mt-5 gap-2">
            <Plus className="h-4 w-4" />
            Create First Rule
          </Button>
        </div>
      ) : (
        <div className="grid gap-4">
          {rules.map((rule, idx) => (
            <div
              key={rule.id}
              className={`flex flex-col gap-4 rounded-xl border p-5 transition-colors sm:flex-row sm:items-center sm:justify-between ${
                rule.enabled
                  ? "border-border/80 bg-card/60 hover:border-border"
                  : "border-border/40 bg-muted/20 opacity-75"
              }`}
            >
              <div className="space-y-2">
                <div className="flex items-center gap-3">
                  <span className="font-semibold text-foreground text-sm tracking-tight">{rule.id}</span>
                  <span
                    className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ${
                      rule.enabled
                        ? "bg-emerald-500/10 text-emerald-500"
                        : "bg-muted text-muted-foreground"
                    }`}
                  >
                    {rule.enabled ? <Check className="h-3 w-3" /> : <X className="h-3 w-3" />}
                    {rule.enabled ? "Active" : "Disabled"}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    Prefix: <code className="rounded bg-muted px-1.5 py-0.5 text-[11px] font-mono">{rule.prefix || "* (all objects)"}</code>
                  </span>
                </div>

                <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-xs text-muted-foreground">
                  {rule.expiration_days > 0 ? (
                    <span className="flex items-center gap-1.5 text-foreground">
                      <Clock className="h-3.5 w-3.5 text-primary" />
                      Expire current after <strong>{rule.expiration_days} days</strong>
                    </span>
                  ) : (
                    <span>Current version: No expiration</span>
                  )}

                  {rule.noncurrent_version_expiration_days > 0 ? (
                    <span className="flex items-center gap-1.5 text-foreground">
                      <History className="h-3.5 w-3.5 text-indigo-400" />
                      Expire noncurrent after <strong>{rule.noncurrent_version_expiration_days} days</strong>
                    </span>
                  ) : (
                    <span>Noncurrent versions: Retained</span>
                  )}

                  {rule.abort_incomplete_multipart_upload_days > 0 && (
                    <span className="flex items-center gap-1.5 text-foreground">
                      <Layers className="h-3.5 w-3.5 text-amber-500" />
                      Abort multipart after <strong>{rule.abort_incomplete_multipart_upload_days} days</strong>
                    </span>
                  )}
                </div>
              </div>

              <div className="flex items-center gap-2 self-end sm:self-center">
                <Switch
                  checked={rule.enabled}
                  onCheckedChange={() => void handleToggleRule(idx)}
                  aria-label="Toggle rule"
                />
                <Button
                  size="icon"
                  variant="ghost"
                  onClick={() => openEditModal(rule, idx)}
                  className="h-8 w-8 text-muted-foreground hover:text-foreground"
                >
                  <Edit2 className="h-4 w-4" />
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  onClick={() => void handleDeleteRule(idx)}
                  className="h-8 w-8 text-muted-foreground hover:text-destructive"
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Modal Dialog */}
      <Dialog open={modalOpen} onOpenChange={setModalOpen}>
        <DialogContent className="sm:max-w-[540px]">
          <form onSubmit={handleSaveRule}>
            <DialogHeader>
              <DialogTitle>{editingRuleIndex !== null ? "Edit Lifecycle Rule" : "Create Lifecycle Rule"}</DialogTitle>
              <DialogDescription>
                Define lifecycle actions to automate storage retention and cost optimization.
              </DialogDescription>
            </DialogHeader>

            <div className="grid gap-5 py-4">
              <div className="grid grid-cols-4 items-center gap-4">
                <label className="text-right text-xs font-medium text-muted-foreground">Rule ID</label>
                <Input
                  value={ruleId}
                  onChange={(e) => setRuleId(e.target.value)}
                  placeholder="e.g. expire-temp-logs"
                  className="col-span-3"
                  required
                />
              </div>

              <div className="grid grid-cols-4 items-center gap-4">
                <label className="text-right text-xs font-medium text-muted-foreground">Scope / Prefix</label>
                <Input
                  value={prefix}
                  onChange={(e) => setPrefix(e.target.value)}
                  placeholder="e.g. logs/ or temp/ (empty for all)"
                  className="col-span-3"
                />
              </div>

              <div className="grid grid-cols-4 items-center gap-4">
                <label className="text-right text-xs font-medium text-muted-foreground">Status</label>
                <div className="col-span-3 flex items-center gap-3">
                  <Switch checked={enabled} onCheckedChange={setEnabled} />
                  <span className="text-xs text-muted-foreground">{enabled ? "Rule is Enabled" : "Rule is Disabled"}</span>
                </div>
              </div>

              <hr className="border-border/60" />

              {/* Current Version Expiration */}
              <div className="space-y-3 rounded-lg border border-border/60 p-3 bg-muted/20">
                <div className="flex items-center justify-between">
                  <div className="space-y-0.5">
                    <span className="text-xs font-medium text-foreground">Expire Current Version</span>
                    <p className="text-[11px] text-muted-foreground">Permanently delete objects after specified days.</p>
                  </div>
                  <Switch checked={enableExpiration} onCheckedChange={setEnableExpiration} />
                </div>
                {enableExpiration && (
                  <div className="flex items-center gap-2 pt-1">
                    <Input
                      type="number"
                      min={1}
                      max={3650}
                      value={expirationDays}
                      onChange={(e) => setExpirationDays(parseInt(e.target.value) || 0)}
                      className="w-28 h-8 text-xs"
                    />
                    <span className="text-xs text-muted-foreground">days after creation</span>
                  </div>
                )}
              </div>

              {/* Noncurrent Versions Expiration */}
              <div className="space-y-3 rounded-lg border border-border/60 p-3 bg-muted/20">
                <div className="flex items-center justify-between">
                  <div className="space-y-0.5">
                    <span className="text-xs font-medium text-foreground flex items-center gap-1.5">
                      Expire Noncurrent Versions
                      {!versioningEnabled && (
                        <span className="rounded bg-amber-500/10 px-1.5 py-0.2 text-[10px] text-amber-500 font-normal">
                          Requires Versioning
                        </span>
                      )}
                    </span>
                    <p className="text-[11px] text-muted-foreground">Permanently delete older versions when superseded or replaced.</p>
                  </div>
                  <Switch
                    checked={enableNoncurrentExpiration}
                    onCheckedChange={(val) => {
                      if (val && !versioningEnabled) {
                        toast.error("Please enable Bucket Versioning before adding noncurrent version rules.");
                        return;
                      }
                      setEnableNoncurrentExpiration(val);
                    }}
                  />
                </div>
                {enableNoncurrentExpiration && (
                  <div className="flex items-center gap-2 pt-1">
                    <Input
                      type="number"
                      min={1}
                      max={3650}
                      value={noncurrentExpirationDays}
                      onChange={(e) => setNoncurrentExpirationDays(parseInt(e.target.value) || 0)}
                      className="w-28 h-8 text-xs"
                    />
                    <span className="text-xs text-muted-foreground">days after becoming noncurrent</span>
                  </div>
                )}
              </div>

              {/* Abort Incomplete Multipart */}
              <div className="space-y-3 rounded-lg border border-border/60 p-3 bg-muted/20">
                <div className="flex items-center justify-between">
                  <div className="space-y-0.5">
                    <span className="text-xs font-medium text-foreground">Abort Incomplete Multipart Uploads</span>
                    <p className="text-[11px] text-muted-foreground">Clean up abandoned multipart chunks to prevent storage waste.</p>
                  </div>
                  <Switch checked={enableAbortMultipart} onCheckedChange={setEnableAbortMultipart} />
                </div>
                {enableAbortMultipart && (
                  <div className="flex items-center gap-2 pt-1">
                    <Input
                      type="number"
                      min={1}
                      max={365}
                      value={abortMultipartDays}
                      onChange={(e) => setAbortMultipartDays(parseInt(e.target.value) || 0)}
                      className="w-28 h-8 text-xs"
                    />
                    <span className="text-xs text-muted-foreground">days after initiation</span>
                  </div>
                )}
              </div>
            </div>

            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setModalOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={saving}>
                {saving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                {editingRuleIndex !== null ? "Save Changes" : "Create Rule"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
