"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Server } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { getHypervisorEstimate } from "@/features/billing/api";
import { formatMicroUnits } from "@/features/billing/money";
import {
  createVirtualMachine,
  listHypervisorImageCatalog,
  listHypervisorResourcePlans,
  type CreateVirtualMachineInput,
  type VirtualMachine,
} from "@/features/compute/api";
import { useConsoleQueryScope } from "@/shared/query/scope";

export function CreateComputeScreen() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const scope = useConsoleQueryScope();
  const [name, setName] = useState("");
  const [imageID, setImageID] = useState("");
  const [selectedPlanID, setSelectedPlanID] = useState("");
  const [additionalDisks, setAdditionalDisks] = useState<number[]>([]);
  const [sshPublicKey, setSSHPublicKey] = useState("");
  const imageQuery = useQuery({
    queryKey: [...scope, "compute", "image-catalog"],
    queryFn: ({ signal }) => listHypervisorImageCatalog(signal),
    staleTime: 60_000,
  });
  const images = imageQuery.data ?? [];
  const resourcePlanQuery = useQuery({
    queryKey: [...scope, "billing", "hypervisor-resource-plans"],
    queryFn: ({ signal }) => listHypervisorResourcePlans(signal),
    staleTime: 30_000,
  });
  const resourcePlans = resourcePlanQuery.data ?? [];
  const selectedImage = images.find((image) => image.id === imageID) ?? images[0];
  const planEstimateQueries = useQueries({
    queries: resourcePlans.map((plan) => ({
      queryKey: [...scope, "billing", "hypervisor-plan-estimate", plan.plan_id, plan.revision_id],
      queryFn: ({ signal }: { signal: AbortSignal }) => getHypervisorEstimate(
        plan.cpu_cores,
        plan.memory_mib,
        plan.boot_disk_gib,
        signal,
      ),
      staleTime: 30_000,
    })),
  });
  const selectedProfile = resourcePlans.find((plan) => plan.plan_id === selectedPlanID) ?? resourcePlans[0];
  const selectedCPUCores = Number(selectedProfile?.cpu_cores ?? 0);
  const selectedMemoryMIB = Number(selectedProfile?.memory_mib ?? 0);
  const selectedBootDiskGIB = Number(selectedProfile?.boot_disk_gib ?? 0);
  const totalDiskGB = selectedBootDiskGIB + additionalDisks.reduce((total, size) => total + size, 0);
  const validSpecification = additionalDisks.length <= 15
    && additionalDisks.every((size) => Number.isInteger(size) && size >= 8 && size <= 4096)
    && totalDiskGB <= 65536;
  const estimateQuery = useQuery({
    queryKey: [...scope, "billing", "hypervisor-estimate", selectedProfile?.revision_id, totalDiskGB.toString()],
    queryFn: ({ signal }) => getHypervisorEstimate(selectedCPUCores.toString(), selectedMemoryMIB.toString(), totalDiskGB.toString(), signal),
    enabled: Boolean(selectedProfile) && validSpecification,
    staleTime: 30_000,
  });
  const hourlyEstimate = estimateQuery.data
    ? formatMicroUnits(estimateQuery.data.hourly_estimate_micro_units, estimateQuery.data.currency)
    : null;
  const monthlyEstimate = estimateQuery.data
    ? formatMicroUnits(estimateQuery.data.monthly_730_hour_estimate_micro_units, estimateQuery.data.currency)
    : null;

  const mutation = useMutation({
    mutationFn: (input: CreateVirtualMachineInput) => createVirtualMachine(input),
    onSuccess: (vm) => {
      queryClient.setQueryData<VirtualMachine[]>(
        [...scope, "compute", "vms"],
        (current = []) => [vm, ...current.filter((item) => item.id !== vm.id)],
      );
      toast.success(vm.status === "READY" ? "Virtual machine already exists." : "VM provisioning accepted.");
      router.push("/compute");
    },
    onError: (error: unknown) => {
      toast.error(error instanceof Error ? error.message : "VM creation failed.");
    },
  });

  return (
    <div className="w-full pb-10 text-foreground">
      <header className="flex items-center gap-3 border-b border-border pb-5">
        <Button variant="outline" size="icon" className="h-8 w-8" onClick={() => router.push("/compute")}>
          <ArrowLeft className="h-4 w-4" />
        </Button>
        <div className="flex h-9 w-9 items-center justify-center rounded-[6px] border border-blue-500/20 bg-blue-600/10 text-blue-500">
          <Server className="h-4.5 w-4.5" />
        </div>
        <div>
          <h1 className="text-xl font-bold tracking-tight">Create Virtual Machine</h1>
          <p className="mt-0.5 text-xs font-medium text-muted-foreground">
            The VM name is the workspace-scoped request identity. Repeating the same specification resumes the same operation.
          </p>
        </div>
      </header>

      <form
        className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-12"
        onSubmit={(event) => {
          event.preventDefault();
          const normalizedName = name.trim().toLowerCase();
          if (!/^[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(normalizedName) || normalizedName.includes("--")) {
            toast.error("Use 1-63 lowercase letters, numbers or single hyphens.");
            return;
          }
          const key = sshPublicKey.trim();
          if (!key.startsWith("ssh-ed25519 ") && !key.startsWith("ssh-rsa ") && !key.startsWith("ecdsa-sha2-")) {
            toast.error("Enter a valid SSH public key.");
            return;
          }
          if (!selectedImage) {
            toast.error("Select an AVAILABLE image from the Zone catalog.");
            return;
          }
          if (!selectedProfile) {
            toast.error("No active resource plan is available in this Zone.");
            return;
          }
          mutation.mutate({
            name: normalizedName,
            image_id: selectedImage.id,
            resource_plan_id: selectedProfile.plan_id,
            resource_plan_revision_id: selectedProfile.revision_id,
            additional_disks: additionalDisks.map((sizeGB) => ({ size_gb: String(sizeGB) })),
            ssh_public_key: key,
          });
        }}
      >
        <section className="space-y-5 lg:col-span-8">
          <div className="rounded-[6px] border border-border">
            <div className="border-b border-border px-5 py-3">
              <h2 className="text-sm font-semibold">Identity and image</h2>
            </div>
            <div className="grid gap-5 p-5 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="vm-name">VM name</Label>
                <Input id="vm-name" value={name} maxLength={63} onChange={(event) => setName(event.target.value)} placeholder="api-worker-01" required />
              </div>
              <div className="space-y-2">
                <Label htmlFor="vm-image">Operating system</Label>
                <select
                  id="vm-image"
                  value={selectedImage?.id ?? ""}
                  onChange={(event) => setImageID(event.target.value)}
                  disabled={imageQuery.isLoading || images.length === 0}
                  className="flex h-9 w-full rounded-md border border-input bg-background px-3 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  {images.map((image) => (
                    <option key={image.id} value={image.id}>
                      {image.name} · {image.distribution} {image.release} · r{image.revision}
                    </option>
                  ))}
                </select>
                {imageQuery.isError && <p className="text-xs text-red-500">Image catalog is unavailable.</p>}
                {!imageQuery.isError && !imageQuery.isLoading && images.length === 0 && (
                  <p className="text-xs text-amber-600">No AVAILABLE image exists in this Zone.</p>
                )}
              </div>
            </div>
          </div>

          <div className="rounded-[6px] border border-border">
            <div className="border-b border-border px-5 py-3">
              <h2 className="text-sm font-semibold">Choose a plan</h2>
            </div>
            <div className="grid gap-3 p-5 sm:grid-cols-3">
              {resourcePlans.map((plan, index) => {
                const pricing = planEstimateQueries[index];
                const monthly = pricing.data
                  ? formatMicroUnits(pricing.data.monthly_730_hour_estimate_micro_units, pricing.data.currency)
                  : null;
                return (
                  <button
					key={plan.revision_id}
                    type="button"
                    aria-pressed={(selectedProfile?.plan_id ?? "") === plan.plan_id}
                    disabled={pricing.isError}
                    onClick={() => {
                      setSelectedPlanID(plan.plan_id);
                    }}
                    className={`rounded-[6px] border p-4 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
                      (selectedProfile?.plan_id ?? "") === plan.plan_id
                        ? "border-blue-500 bg-blue-500/5 ring-1 ring-blue-500"
                        : "border-border hover:border-blue-500/50"
                    }`}
                  >
                    <span className="block text-sm font-semibold">{plan.display_name}</span>
                    <span className="mt-1 block min-h-8 text-[11px] text-muted-foreground">{plan.description}</span>
                    <span className="mt-3 block text-xs font-medium">
                      {plan.cpu_cores} vCPU · {Number(plan.memory_mib) / 1024} GiB · {plan.boot_disk_gib} GiB
                    </span>
                    <span className="mt-2 block text-sm font-semibold text-blue-500">
                      {monthly ?? (pricing.isLoading ? "Loading…" : "Unavailable")}
                      {monthly && <span className="text-[10px] font-normal text-muted-foreground"> / 730h</span>}
                    </span>
                  </button>
                );
              })}
            </div>
            <div className="border-t border-border px-5 py-3">
              <h3 className="text-xs font-semibold">Additional data disks</h3>
              <p className="mt-0.5 text-[11px] text-muted-foreground">CPU, memory and boot disk are fixed by the selected resource plan. You may add up to 15 data disks.</p>
            </div>
            <div className="space-y-3 p-5">
              {additionalDisks.map((size, index) => (
                <div key={index} className="flex items-end gap-2">
                  <div className="flex-1 space-y-2"><Label htmlFor={`vm-disk-${index}`}>Data disk {index + 1} (GiB)</Label><Input id={`vm-disk-${index}`} type="number" min={8} max={4096} value={size} onChange={(event) => setAdditionalDisks((current) => current.map((item, currentIndex) => currentIndex === index ? Number(event.target.value) : item))} required /></div>
                  <Button type="button" variant="outline" onClick={() => setAdditionalDisks((current) => current.filter((_, currentIndex) => currentIndex !== index))}>Remove</Button>
                </div>
              ))}
              <Button type="button" variant="outline" disabled={additionalDisks.length >= 15} onClick={() => setAdditionalDisks((current) => [...current, 32])}>Add data disk</Button>
            </div>
          </div>

          <div className="rounded-[6px] border border-border">
            <div className="border-b border-border px-5 py-3">
              <h2 className="text-sm font-semibold">Access</h2>
            </div>
            <div className="space-y-2 p-5">
              <Label htmlFor="vm-ssh-key">SSH public key</Label>
              <Textarea id="vm-ssh-key" value={sshPublicKey} maxLength={16384} rows={5} onChange={(event) => setSSHPublicKey(event.target.value)} placeholder="ssh-ed25519 AAAA…" required />
              <p className="text-[11px] text-muted-foreground">Only the public key is sent to the selected Zone. Never paste a private key.</p>
            </div>
          </div>
        </section>

        <aside className="self-start rounded-[6px] border border-border lg:col-span-4">
          <div className="border-b border-border px-5 py-3">
            <h2 className="text-sm font-semibold">Request summary</h2>
          </div>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-3 p-5 text-[13px]">
            <dt className="text-muted-foreground">Name</dt><dd className="text-right font-medium">{name.trim().toLowerCase() || "—"}</dd>
            <dt className="text-muted-foreground">Image</dt><dd className="text-right font-medium">{selectedImage?.name ?? "—"}</dd>
            <dt className="text-muted-foreground">vCPU</dt><dd className="text-right font-medium">{selectedCPUCores || "—"}</dd>
            <dt className="text-muted-foreground">Memory</dt><dd className="text-right font-medium">{selectedMemoryMIB || "—"} MiB</dd>
            <dt className="text-muted-foreground">Boot disk</dt><dd className="text-right font-medium">{selectedBootDiskGIB || "—"} GiB</dd>
            <dt className="text-muted-foreground">Data disks</dt><dd className="text-right font-medium">{additionalDisks.length} · {totalDiskGB - selectedBootDiskGIB} GiB</dd>
            <dt className="text-muted-foreground">Hourly price</dt><dd className="text-right font-medium">{hourlyEstimate ?? (estimateQuery.isLoading ? "Loading…" : "—")}</dd>
            <dt className="text-muted-foreground">Monthly estimate</dt><dd className="text-right font-medium">{monthlyEstimate ?? (estimateQuery.isLoading ? "Loading…" : "—")}</dd>
          </dl>
          <div className="border-t border-border px-5 py-3 text-[11px] text-muted-foreground">
            CPU, memory and disk are billed from selected limits for every allocated second, whether the VM is running or stopped. Monthly estimate uses 730 hours; network usage is separate.
            {estimateQuery.isError && <p className="mt-2 text-red-500">Pricing is unavailable. An operator must publish all active Hypervisor schedules before VM creation.</p>}
          </div>
          <div className="flex gap-2 border-t border-border p-4">
            <Button type="button" variant="outline" className="flex-1" onClick={() => router.push("/compute")}>Cancel</Button>
            <Button type="submit" className="flex-1" disabled={mutation.isPending || !selectedImage || !selectedProfile || imageQuery.isLoading || resourcePlanQuery.isLoading || !estimateQuery.data || estimateQuery.isFetching}>
              {mutation.isPending ? "Submitting…" : "Create VM"}
            </Button>
          </div>
        </aside>
      </form>
    </div>
  );
}
