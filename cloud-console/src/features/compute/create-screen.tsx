"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Server } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  createVirtualMachine,
  type CreateVirtualMachineInput,
  type VirtualMachine,
} from "@/features/compute/api";
import { useConsoleQueryScope } from "@/shared/query/scope";

export function CreateComputeScreen() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const scope = useConsoleQueryScope();
  const [name, setName] = useState("");
  const [image, setImage] = useState<CreateVirtualMachineInput["image"]>("ubuntu-24.04");
  const [cpuCores, setCpuCores] = useState(2);
  const [memoryMB, setMemoryMB] = useState(4096);
  const [diskGB, setDiskGB] = useState(32);
  const [sshPublicKey, setSSHPublicKey] = useState("");

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
          mutation.mutate({
            name: normalizedName,
            image,
            cpu_cores: cpuCores,
            memory_mb: memoryMB,
            disk_gb: diskGB,
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
                  value={image}
                  onChange={(event) => setImage(event.target.value as CreateVirtualMachineInput["image"])}
                  className="flex h-9 w-full rounded-md border border-input bg-background px-3 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <option value="ubuntu-24.04">Ubuntu 24.04 LTS</option>
                  <option value="debian-12">Debian 12</option>
                </select>
              </div>
            </div>
          </div>

          <div className="rounded-[6px] border border-border">
            <div className="border-b border-border px-5 py-3">
              <h2 className="text-sm font-semibold">Compute specification</h2>
            </div>
            <div className="grid gap-5 p-5 sm:grid-cols-3">
              <div className="space-y-2">
                <Label htmlFor="vm-cpu">vCPU cores</Label>
                <Input id="vm-cpu" type="number" min={1} max={64} value={cpuCores} onChange={(event) => setCpuCores(Number(event.target.value))} required />
              </div>
              <div className="space-y-2">
                <Label htmlFor="vm-memory">Memory (MiB)</Label>
                <Input id="vm-memory" type="number" min={512} max={262144} step={256} value={memoryMB} onChange={(event) => setMemoryMB(Number(event.target.value))} required />
              </div>
              <div className="space-y-2">
                <Label htmlFor="vm-disk">Disk (GiB)</Label>
                <Input id="vm-disk" type="number" min={8} max={4096} value={diskGB} onChange={(event) => setDiskGB(Number(event.target.value))} required />
              </div>
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
            <dt className="text-muted-foreground">Image</dt><dd className="text-right font-medium">{image}</dd>
            <dt className="text-muted-foreground">vCPU</dt><dd className="text-right font-medium">{cpuCores}</dd>
            <dt className="text-muted-foreground">Memory</dt><dd className="text-right font-medium">{memoryMB} MiB</dd>
            <dt className="text-muted-foreground">Disk</dt><dd className="text-right font-medium">{diskGB} GiB</dd>
          </dl>
          <div className="flex gap-2 border-t border-border p-4">
            <Button type="button" variant="outline" className="flex-1" onClick={() => router.push("/compute")}>Cancel</Button>
            <Button type="submit" className="flex-1" disabled={mutation.isPending}>
              {mutation.isPending ? "Submitting…" : "Create VM"}
            </Button>
          </div>
        </aside>
      </form>
    </div>
  );
}
