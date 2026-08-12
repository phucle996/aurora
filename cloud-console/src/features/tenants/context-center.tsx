"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { ArrowLeft, Building2, Loader2, UserRound } from "lucide-react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

import { useWorkspace } from "@/context/WorkspaceContext";
import { listTenants, switchToPersonal, switchToTenant, type TenantCatalogItem } from "@/features/tenants/api";
import { useUserSession } from "@/session/use-session";

export function PersonalContextCenter() {
  const router = useRouter();
  const { renderContext, refreshSession } = useUserSession();
  const { clearWorkspaceContext } = useWorkspace();
  const tenants = useQuery({ queryKey: ["personal", "tenant-catalog"], queryFn: ({ signal }) => listTenants(signal) });
  const tenantItems = tenants.data ?? [];

  const selectTenant = async (tenant: TenantCatalogItem) => {
    try {
      await switchToTenant(tenant);
      clearWorkspaceContext();
      await refreshSession();
      router.replace("/tenant");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Tenant switch failed");
    }
  };

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <div>
        <h1 className="text-xl font-semibold">Window Context</h1>
        <p className="mt-1 text-sm text-muted-foreground">You are in Personal. Choose a tenant to open its isolated control-plane context.</p>
      </div>
      <div className="rounded-lg border border-border bg-card p-4">
        <div className="mb-4 flex items-center gap-2 text-sm font-medium"><UserRound className="h-4 w-4 text-blue-500" /> Personal context (active)</div>
        {tenants.isLoading ? <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /> : tenants.isError ? <p className="text-sm text-destructive">Unable to load your tenant memberships.</p> : tenantItems.length === 0 ? <p className="text-sm text-muted-foreground">No tenant memberships are available.</p> : <div className="grid gap-2">{tenantItems.map((tenant) => <button key={tenant.id} type="button" onClick={() => void selectTenant(tenant)} className="flex items-center justify-between rounded-md border border-border p-3 text-left hover:bg-muted"><span className="flex items-center gap-3"><Building2 className="h-4 w-4 text-muted-foreground" /><span><span className="block text-sm font-medium">{tenant.name}</span><span className="block text-xs text-muted-foreground">{tenant.primary_domain} · {tenant.role_name || `level ${tenant.role_level}`}</span></span></span><span className="text-xs text-blue-500">Open</span></button>)}</div>}
      </div>
      {renderContext?.kind !== "personal" && <p className="text-sm text-destructive">The active session is not personal. Return to the current tenant context before using this page.</p>}
    </div>
  );
}

export function TenantContextCenter() {
  const router = useRouter();
  const { refreshSession } = useUserSession();
  const { clearWorkspaceContext } = useWorkspace();
  const [switching, setSwitching] = useState(false);

  const goPersonal = async () => {
    setSwitching(true);
    try {
      await switchToPersonal();
      clearWorkspaceContext();
      await refreshSession();
      router.replace("/personal");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Context switch failed");
    } finally {
      setSwitching(false);
    }
  };

  return <div className="mx-auto max-w-3xl space-y-6"><div><h1 className="text-xl font-semibold">Window Context</h1><p className="mt-1 text-sm text-muted-foreground">You are in a tenant context. Return to Personal before selecting another tenant.</p></div><div className="rounded-lg border border-border bg-card p-4"><p className="text-sm font-medium">Tenant context active</p><p className="mt-1 text-sm text-muted-foreground">Tenant-to-tenant switching is intentionally blocked to prevent authority and workspace leakage.</p><button type="button" onClick={() => void goPersonal()} disabled={switching} className="mt-4 inline-flex items-center gap-2 rounded-md bg-blue-600 px-3 py-2 text-sm font-medium text-white disabled:opacity-50">{switching ? <Loader2 className="h-4 w-4 animate-spin" /> : <ArrowLeft className="h-4 w-4" />} Return to Personal</button></div></div>;
}
