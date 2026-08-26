import { fetchJSON } from "@/shared/api/http";
import { criticalFetchJSON } from "@/shared/api/critical";

export type VMStatus = "PROVISIONING" | "READY" | "DELETING";

export type VirtualMachine = {
  id: string;
  operation_id: string;
  name: string;
  image: string;
  image_id?: string | null;
  image_revision: string;
  resource_plan_id: string;
  resource_plan_revision_id: string;
  resource_plan_revision_number: string;
  cpu_cores: number;
  memory_mb: string;
  boot_disk_gb: string;
  disk_gb: string;
  additional_disk_sizes_gb: string[];
  status: VMStatus;
  zone_id: string;
  provider_vmid: string;
  ipv4_address?: string | null;
  created_at: string;
  updated_at: string;
  provisioned_at?: string | null;
};

export type CreateVirtualMachineInput = {
  name: string;
  image_id: string;
  resource_plan_id: string;
  resource_plan_revision_id: string;
  additional_disks: Array<{ size_gb: string }>;
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

export type HypervisorResourcePlan = {
  plan_id: string;
  revision_id: string;
  revision_number: string;
  code: string;
  display_name: string;
  description: string;
  billing_model: "LIMIT_HOURLY";
  cpu_cores: string;
  memory_mib: string;
  boot_disk_gib: string;
  content_sha256: string;
  effective_from: string;
};

export async function listHypervisorResourcePlans(signal?: AbortSignal): Promise<HypervisorResourcePlan[]> {
  const response = await fetchJSON<{ data?: { plans?: HypervisorResourcePlan[] } }>(
    "/api/v1/billing/wallet/hypervisor/resource-plans?limit=100",
    { method: "GET", signal },
  );
  return response.data?.plans ?? [];
}

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
  const response = await criticalFetchJSON<{ data?: VirtualMachine }>("/api/v1/critical/hypervisor/vms", {
    method: "POST",
    body: input,
    signal,
  });
  if (!response.data) throw new Error("The VM create response did not contain a resource.");
  return response.data;
}

export async function deleteVirtualMachine(id: string, signal?: AbortSignal): Promise<{ id: string; operation_id: string; status: "DELETING" }> {
  const response = await criticalFetchJSON<{ data?: { id: string; operation_id: string; status: "DELETING" } }>(`/api/v1/critical/hypervisor/vms/${encodeURIComponent(id)}`, {
    method: "DELETE",
    signal,
  });
  if (!response.data) throw new Error("The VM delete response did not contain an operation.");
  return response.data;
}
