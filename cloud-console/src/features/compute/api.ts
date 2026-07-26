import { fetchJSON } from "@/shared/api/http";

export type VMStatus = "PROVISIONING" | "READY" | "FAILED";

export type VirtualMachine = {
  id: string;
  operation_id: string;
  name: string;
  image: string;
  cpu_cores: number;
  memory_mb: number;
  disk_gb: number;
  status: VMStatus;
  zone_id: string;
  provider_node?: string | null;
  provider_vmid?: number | null;
  ipv4_address?: string | null;
  error_code?: string | null;
  error_message?: string | null;
  created_at: string;
  updated_at: string;
  provisioned_at?: string | null;
};

export type CreateVirtualMachineInput = {
  name: string;
  image: "ubuntu-24.04" | "debian-12";
  cpu_cores: number;
  memory_mb: number;
  disk_gb: number;
  ssh_public_key: string;
};

export async function listVirtualMachines(signal?: AbortSignal): Promise<VirtualMachine[]> {
  const response = await fetchJSON<{ data?: { vms?: VirtualMachine[] } }>(
    "/api/v1/hypervisor/vms?limit=100",
    { method: "GET", signal },
  );
  return response.data?.vms ?? [];
}

export async function createVirtualMachine(
  input: CreateVirtualMachineInput,
  signal?: AbortSignal,
): Promise<VirtualMachine> {
  const response = await fetchJSON<{ data?: VirtualMachine }>("/api/v1/hypervisor/vms", {
    method: "POST",
    body: input,
    signal,
  });
  if (!response.data) throw new Error("The VM create response did not contain a resource.");
  return response.data;
}
