"use client";

import { useEffect, useMemo, useState } from "react";
import { Loader2, Plus, RefreshCw, Save, ShieldCheck } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  createTenantRole,
  createTenantRoleRevision,
  getTenantRole,
  listTenantPermissions,
  listTenantRoles,
  upgradeTenantRoleAssignments,
  type TenantPermission,
  type TenantRole,
  type TenantRoleDetails,
} from "@/features/tenants/api";
import { useUserSession } from "@/session/use-session";

type Draft = { code: string; name: string; description: string; roleLevel: string; permissionIDs: string[] };
const emptyDraft: Draft = { code: "", name: "", description: "", roleLevel: "10", permissionIDs: [] };

export function TenantRbacScreen() {
	const { checkPermission, renderContext } = useUserSession();
  const [roles, setRoles] = useState<TenantRole[]>([]);
  const [permissions, setPermissions] = useState<TenantPermission[]>([]);
  const [selected, setSelected] = useState<TenantRoleDetails | null>(null);
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [creating, setCreating] = useState(false);
	const [loading, setLoading] = useState(true);
	const [saving, setSaving] = useState(false);
	const canReadPermissions = checkPermission("iam:permissions", "read");
	const canWriteRole = checkPermission("iam:role", "write") && canReadPermissions;
	const canAssignRole = checkPermission("iam:role", "assign");

  const permissionGroups = useMemo(() => {
    const groups = new Map<string, TenantPermission[]>();
    for (const permission of permissions) {
      const items = groups.get(permission.module) ?? [];
      items.push(permission);
      groups.set(permission.module, items);
    }
    return Array.from(groups.entries()).sort(([left], [right]) => left.localeCompare(right));
  }, [permissions]);

	async function reload(preferredRoleID?: string) {
		setLoading(true);
		try {
			const roleItems = await listTenantRoles();
			setRoles(roleItems);
			if (canReadPermissions) {
				try {
					setPermissions(await listTenantPermissions());
				} catch (error) {
					setPermissions([]);
					toast.error(error instanceof Error ? error.message : "Cannot load the tenant permission catalog.");
				}
			} else {
				setPermissions([]);
			}
			const roleID = preferredRoleID ?? selected?.id ?? roleItems[0]?.id;
      if (roleID) {
        const detail = await getTenantRole(roleID);
        setSelected(detail);
        setCreating(false);
        setDraft({ code: detail.code, name: detail.name, description: detail.description, roleLevel: String(detail.role_level), permissionIDs: detail.permissions.map((item) => item.id) });
			} else {
				setSelected(null);
				setCreating(canWriteRole);
				setDraft(emptyDraft);
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Cannot load tenant access control.");
    } finally {
      setLoading(false);
    }
  }

	useEffect(() => {
		const controller = new AbortController();
		void listTenantRoles(controller.signal)
			.then(async (roleItems) => {
				setRoles(roleItems);
				if (canReadPermissions) {
					try {
						setPermissions(await listTenantPermissions(controller.signal));
					} catch (error) {
						if (!controller.signal.aborted) toast.error(error instanceof Error ? error.message : "Cannot load the tenant permission catalog.");
					}
				}
				const roleID = roleItems[0]?.id;
				if (!roleID) {
					setCreating(canWriteRole);
					return;
        }
        const detail = await getTenantRole(roleID, controller.signal);
        setSelected(detail);
        setDraft({ code: detail.code, name: detail.name, description: detail.description, roleLevel: String(detail.role_level), permissionIDs: detail.permissions.map((item) => item.id) });
      })
      .catch((error) => {
        if (!controller.signal.aborted) toast.error(error instanceof Error ? error.message : "Cannot load tenant access control.");
      })
			.finally(() => { if (!controller.signal.aborted) setLoading(false); });
		return () => controller.abort();
	}, [canReadPermissions, canWriteRole]);

  async function selectRole(roleID: string) {
    setLoading(true);
    try {
      const detail = await getTenantRole(roleID);
      setSelected(detail);
      setCreating(false);
      setDraft({ code: detail.code, name: detail.name, description: detail.description, roleLevel: String(detail.role_level), permissionIDs: detail.permissions.map((item) => item.id) });
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Cannot load this tenant role.");
    } finally {
      setLoading(false);
    }
  }

  if (renderContext?.kind !== "tenant") {
    return <Card><CardHeader><CardTitle>Tenant context required</CardTitle><CardDescription>Switch to a tenant before managing tenant roles.</CardDescription></CardHeader></Card>;
  }

  return <div className="space-y-5">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div><h1 className="text-xl font-semibold">Tenant access control</h1><p className="mt-1 text-sm text-muted-foreground">Role changes create an immutable revision. Existing members stay pinned until you explicitly roll it out.</p></div>
		{canWriteRole && <Button variant="outline" onClick={() => { setCreating(true); setSelected(null); setDraft(emptyDraft); }}><Plus /> New role</Button>}
    </div>
    <div className="grid gap-5 lg:grid-cols-[320px_minmax(0,1fr)]">
      <Card><CardHeader><CardTitle>Roles</CardTitle><CardDescription>{roles.length} tenant-owned roles</CardDescription></CardHeader><CardContent className="space-y-2">
        {loading && roles.length === 0 ? <Loader2 className="h-5 w-5 animate-spin" /> : roles.map((role) => <button key={role.id} type="button" onClick={() => void selectRole(role.id)} className="w-full rounded-md border p-3 text-left hover:bg-muted">
          <span className="flex items-center justify-between gap-2"><span className="font-medium">{role.name}</span><Badge variant="outline">r{role.version}</Badge></span>
          <span className="mt-1 block text-xs text-muted-foreground">level {role.role_level} · {role.permissions_count} permissions · {role.assignments_count} assignments</span>
          {role.outdated_assignments_count > 0 && <span className="mt-1 block text-xs text-amber-600">{role.outdated_assignments_count} pinned to an older revision</span>}
        </button>)}
      </CardContent></Card>
      <Card><CardHeader><CardTitle>{creating ? "Create tenant role" : selected ? `${selected.name} · r${selected.version}` : "Select a role"}</CardTitle><CardDescription>{creating ? "The first immutable revision will be r1." : "Saving creates the next revision; it does not alter current memberships."}</CardDescription></CardHeader>
		{(creating || selected) && <CardContent><form className="space-y-5" onSubmit={async (event) => {
			event.preventDefault();
			if (!canWriteRole) { toast.error("You do not have permission to change tenant roles."); return; }
          const roleLevel = Number(draft.roleLevel);
          if (!draft.name.trim() || !Number.isInteger(roleLevel) || roleLevel < 4 || roleLevel > 99 || draft.permissionIDs.length === 0) { toast.error("Name, level 4–99, and at least one permission are required."); return; }
          setSaving(true);
          try {
            const input = { name: draft.name.trim(), description: draft.description.trim(), role_level: roleLevel, permission_ids: draft.permissionIDs };
            if (creating) {
              const code = draft.code.trim().toLowerCase();
              if (!/^[a-z0-9_]{2,100}$/.test(code) || code === "tenant_root") throw new Error("Role code must use lowercase letters, numbers, or underscores.");
              await createTenantRole(code, input);
              toast.success("Tenant role r1 created.");
              await reload();
            } else if (selected) {
              await createTenantRoleRevision(selected.id, selected.version, input);
              toast.success(`Revision r${selected.version + 1} created. Existing assignments were not changed.`);
              await reload(selected.id);
            }
          } catch (error) { toast.error(error instanceof Error ? error.message : "Cannot save tenant role."); } finally { setSaving(false); }
        }}>
			<div className="grid gap-4 sm:grid-cols-2"><div className="space-y-2"><Label htmlFor="tenant-role-code">Code</Label><Input id="tenant-role-code" value={draft.code} disabled={!creating || saving || !canWriteRole} onChange={(event) => setDraft((value) => ({ ...value, code: event.target.value }))} /></div><div className="space-y-2"><Label htmlFor="tenant-role-level">Role level</Label><Input id="tenant-role-level" type="number" min={4} max={99} value={draft.roleLevel} disabled={saving || !canWriteRole} onChange={(event) => setDraft((value) => ({ ...value, roleLevel: event.target.value }))} /></div></div>
			<div className="space-y-2"><Label htmlFor="tenant-role-name">Name</Label><Input id="tenant-role-name" value={draft.name} disabled={saving || !canWriteRole} onChange={(event) => setDraft((value) => ({ ...value, name: event.target.value }))} /></div>
			<div className="space-y-2"><Label htmlFor="tenant-role-description">Description</Label><Textarea id="tenant-role-description" value={draft.description} disabled={saving || !canWriteRole} onChange={(event) => setDraft((value) => ({ ...value, description: event.target.value }))} /></div>
			<div className="space-y-3"><Label>Permissions</Label>{canWriteRole ? permissionGroups.map(([module, items]) => <div key={module} className="rounded-md border p-3"><p className="mb-2 text-sm font-medium">{module}</p><div className="grid gap-2 sm:grid-cols-2">{items.map((permission) => <label key={permission.id} className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={draft.permissionIDs.includes(permission.id)} disabled={saving} onChange={(event) => setDraft((value) => ({ ...value, permissionIDs: event.target.checked ? [...value.permissionIDs, permission.id] : value.permissionIDs.filter((id) => id !== permission.id) }))} /><span><span className="block font-mono text-xs">{permission.object}:{permission.behavior}</span><span className="text-xs text-muted-foreground">{permission.description}</span></span></label>)}</div></div>) : <div className="flex flex-wrap gap-2">{selected?.permissions.map((permission) => <Badge key={permission.id} variant="outline">{permission.module}:{permission.object}:{permission.behavior}</Badge>)}</div>}</div>
			<div className="flex flex-wrap gap-2">{canWriteRole && <Button type="submit" disabled={saving}>{saving ? <Loader2 className="animate-spin" /> : <Save />}{creating ? "Create r1" : `Create r${(selected?.version ?? 0) + 1}`}</Button>}
				{canAssignRole && !creating && selected && selected.outdated_assignments_count > 0 && <Button type="button" variant="destructive" disabled={saving} onClick={async () => { if (!window.confirm(`Move ${selected.outdated_assignments_count} assignments to r${selected.version}? This may revoke tenant permissions immediately.`)) return; setSaving(true); try { const result = await upgradeTenantRoleAssignments(selected.id); toast.success(`${result.updated_assignments_count} assignments moved to r${result.version}.`); await reload(selected.id); } catch (error) { toast.error(error instanceof Error ? error.message : "Cannot roll out this revision."); } finally { setSaving(false); } }}><RefreshCw /> Roll out r{selected.version} to {selected.outdated_assignments_count}</Button>}
            {!creating && selected && selected.outdated_assignments_count === 0 && <span className="inline-flex items-center gap-1 text-sm text-emerald-600"><ShieldCheck className="h-4 w-4" /> All assignments use r{selected.version}</span>}
          </div>
        </form></CardContent>}
      </Card>
    </div>
  </div>;
}
