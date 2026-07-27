import { fetchJSON } from "@/shared/api/http";

export type VMStatus = "PROVISIONING" | "READY";

export type VirtualMachine = {
  id: string;
  operation_id: string;
  name: string;
  image: string;
  image_id?: string | null;
  image_revision?: number | null;
  cpu_cores: number;
  memory_mb: number;
  disk_gb: number;
  status: VMStatus;
  zone_id: string;
  provider_vmid?: number | null;
  ipv4_address?: string | null;
  created_at: string;
  updated_at: string;
  provisioned_at?: string | null;
};

export type CreateVirtualMachineInput = {
  name: string;
  image_id: string;
  cpu_cores: number;
  memory_mb: number;
  disk_gb: number;
  ssh_public_key: string;
};

export type HypervisorImageCatalogItem = {
  id: string;
  zone_id: string;
  name: string;
  code: string;
  distribution: string;
  release: string;
  revision: number;
  architecture: "x86_64" | "aarch64";
  format: "qcow2" | "raw";
  size_bytes: number;
};

export async function listHypervisorImageCatalog(signal?: AbortSignal): Promise<HypervisorImageCatalogItem[]> {
  const response = await fetchJSON<{ data?: { images?: HypervisorImageCatalogItem[] } }>(
    "/api/v1/hypervisor/images/catalog",
    { method: "GET", signal },
  );
  return response.data?.images ?? [];
}

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
