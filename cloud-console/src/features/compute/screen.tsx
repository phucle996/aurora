"use client";

import { useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { Cpu, Plus, RefreshCw, Search, Server, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useWorkspace } from "@/context/WorkspaceContext";
import { listVirtualMachines, type VMStatus } from "@/features/compute/api";
import { useComputeRealtime } from "@/features/compute/realtime";
import { useConsoleQueryScope } from "@/shared/query/scope";
import { useUserSession } from "@/session/use-session";

export function ComputeScreen() {
  const router = useRouter();
  const { activeWorkspaceID, loading: workspaceLoading } = useWorkspace();
  const { checkPermission } = useUserSession();
  const scope = useConsoleQueryScope();
  const queryKey = useMemo(() => [...scope, "compute", "vms"] as const, [scope]);
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState<"ALL" | VMStatus>("ALL");
  const [selectedID, setSelectedID] = useState<string | null>(null);

  const { data = [], error, isError, isLoading, isRefetching, refetch } = useQuery({
    queryKey,
    queryFn: ({ signal }) => listVirtualMachines(signal),
    enabled: Boolean(activeWorkspaceID) && !workspaceLoading,
    staleTime: 15_000,
  });
  useComputeRealtime(queryKey);

  const filtered = useMemo(() => {
    const term = search.trim().toLowerCase();
    return data.filter((vm) => {
      if (status !== "ALL" && vm.status !== status) return false;
      return !term || vm.name.toLowerCase().includes(term) || vm.id.toLowerCase().includes(term);
    });
  }, [data, search, status]);
  const selected = data.find((vm) => vm.id === selectedID) ?? null;

  return (
    <div className="flex min-h-[calc(100vh-110px)] w-full items-stretch pb-10 text-foreground">
      <section className={selected ? "min-w-0 flex-1 lg:w-2/3 lg:flex-none lg:pr-6" : "min-w-0 flex-1"}>
        <header className="flex flex-col gap-4 border-b border-border pb-5 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-start gap-3">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-[6px] border border-blue-500/20 bg-blue-600/10 text-blue-500">
              <Server className="h-5 w-5" />
            </div>
            <div>
              <h1 className="text-xl font-bold tracking-tight">Virtual Machines</h1>
              <p className="mt-1 max-w-xl text-xs font-medium leading-relaxed text-muted-foreground">
                Provision and inspect Proxmox-backed compute resources in the active workspace and Zone.
              </p>
            </div>
          </div>
          <Button
            variant="outline"
            size="sm"
            disabled={isLoading || isRefetching || workspaceLoading}
            onClick={() => void refetch()}
          >
            <RefreshCw className={isRefetching ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} />
            Sync
          </Button>
        </header>

        <div className="mt-5 overflow-hidden rounded-[6px] border border-border">
          <div className="flex flex-col gap-2 border-b border-border p-3 sm:flex-row sm:items-center">
            <div className="relative min-w-0 flex-1 sm:max-w-[420px]">
              <Search className="absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="Search VM name or ID"
                className="h-9 pl-9 text-[13px]"
              />
            </div>
            <select
              value={status}
              onChange={(event) => setStatus(event.target.value as "ALL" | VMStatus)}
              className="h-9 rounded-md border border-input bg-background px-3 text-[12px] font-semibold outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <option value="ALL">Status: All</option>
              <option value="PROVISIONING">Status: Provisioning</option>
              <option value="READY">Status: Ready</option>
              <option value="FAILED">Status: Failed</option>
            </select>
            {checkPermission("hypervisor:vm", "create") && (
              <Button size="sm" className="sm:ml-auto" onClick={() => router.push("/compute/new")}>
                <Plus className="h-3.5 w-3.5" />
                Create VM
              </Button>
            )}
          </div>

          {isLoading ? (
            <div className="p-16 text-center text-xs font-semibold text-muted-foreground">Loading virtual machines…</div>
          ) : isError ? (
            <div className="p-12 text-center">
              <p className="text-sm font-semibold text-red-500">Virtual machines could not be loaded</p>
              <p className="mx-auto mt-1 max-w-lg text-xs text-muted-foreground">
                {error instanceof Error ? error.message : "The compute API is temporarily unavailable."}
              </p>
              <Button variant="outline" size="sm" className="mt-4" onClick={() => void refetch()}>
                Try again
              </Button>
            </div>
          ) : filtered.length === 0 ? (
            <div className="p-16 text-center">
              <Cpu className="mx-auto mb-3 h-9 w-9 text-muted-foreground/60" />
              <p className="text-sm font-semibold">No virtual machines found</p>
              <p className="mt-1 text-xs text-muted-foreground">Create a VM or adjust the current filters.</p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[760px] text-left text-[13px]">
                <thead className="border-b border-border bg-muted/20 text-[11px] font-bold uppercase tracking-wider text-muted-foreground">
                  <tr>
                    <th className="px-4 py-3">Name</th>
                    <th className="px-4 py-3">Status</th>
                    <th className="px-4 py-3">Image</th>
                    <th className="px-4 py-3">vCPU</th>
                    <th className="px-4 py-3">Memory</th>
                    <th className="px-4 py-3">Disk</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border/60">
                  {filtered.map((vm) => (
                    <tr
                      key={vm.id}
                      onClick={() => setSelectedID(vm.id)}
                      className={`cursor-pointer transition-colors hover:bg-muted/40 ${selectedID === vm.id ? "bg-blue-500/5" : ""}`}
                    >
                      <td className="px-4 py-3">
                        <p className="text-sm font-semibold">{vm.name}</p>
                        <p className="mt-0.5 font-mono text-[10px] text-muted-foreground">{vm.id}</p>
                      </td>
                      <td className="px-4 py-3">
                        <span className="inline-flex items-center gap-2 font-semibold">
                          <span className={`h-2 w-2 rounded-full ${
                            vm.status === "READY" ? "bg-emerald-500" :
                              vm.status === "FAILED" ? "bg-red-500" : "bg-amber-500"
                          }`} />
                          {vm.status === "READY" ? "Ready" : vm.status === "FAILED" ? "Failed" : "Provisioning"}
                        </span>
                      </td>
                      <td className="px-4 py-3 text-muted-foreground">{vm.image}</td>
                      <td className="px-4 py-3 font-semibold">{vm.cpu_cores}</td>
                      <td className="px-4 py-3 font-semibold">{Math.round(vm.memory_mb / 1024)} GiB</td>
                      <td className="px-4 py-3 font-semibold">{vm.disk_gb} GiB</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </section>

      {selected && (
        <aside className="fixed inset-x-0 bottom-0 z-40 max-h-[75vh] overflow-y-auto border-t border-border bg-background p-5 shadow-sm lg:static lg:z-auto lg:max-h-none lg:w-1/3 lg:border-l lg:border-t-0 lg:bg-transparent lg:pl-6 lg:pr-0 lg:shadow-none">
          <div className="flex items-start justify-between border-b border-border/60 pb-4">
            <div>
              <p className="text-[11px] font-bold uppercase tracking-wider text-muted-foreground">Virtual machine</p>
              <h2 className="mt-1 text-base font-semibold">{selected.name}</h2>
            </div>
            <Button
              variant="ghost"
              size="icon"
              className="h-8 w-8"
              aria-label="Close virtual machine details"
              onClick={() => setSelectedID(null)}
            >
              <X className="h-4 w-4" />
            </Button>
          </div>
          <div className="border-b border-border/60 py-5">
            <p className="mb-3 text-[11px] font-bold uppercase tracking-wider">Configuration</p>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-[13px]">
              <dt className="text-muted-foreground">Image</dt><dd className="text-right font-medium">{selected.image}</dd>
              <dt className="text-muted-foreground">vCPU</dt><dd className="text-right font-medium">{selected.cpu_cores}</dd>
              <dt className="text-muted-foreground">Memory</dt><dd className="text-right font-medium">{selected.memory_mb} MiB</dd>
              <dt className="text-muted-foreground">Disk</dt><dd className="text-right font-medium">{selected.disk_gb} GiB</dd>
            </dl>
          </div>
          <div className="border-b border-border/60 py-5">
            <p className="mb-3 text-[11px] font-bold uppercase tracking-wider">Runtime</p>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-[13px]">
              <dt className="text-muted-foreground">Node</dt><dd className="text-right font-medium">{selected.provider_node || "Pending"}</dd>
              <dt className="text-muted-foreground">Provider VMID</dt><dd className="text-right font-medium">{selected.provider_vmid || "Pending"}</dd>
              <dt className="text-muted-foreground">IPv4</dt><dd className="text-right font-mono text-xs">{selected.ipv4_address || "Pending"}</dd>
            </dl>
          </div>
          {selected.status === "FAILED" && (
            <div className="py-5">
              <p className="mb-2 text-[11px] font-bold uppercase tracking-wider text-red-500">Provisioning failure</p>
              <p className="text-xs leading-relaxed text-muted-foreground">{selected.error_message || selected.error_code || "Unknown failure"}</p>
            </div>
          )}
        </aside>
      )}
    </div>
  );
}
