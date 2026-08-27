import { fetchJSON } from "@/shared/api/http";

export type PersonalWalletSummary = {
  wallet_id: string;
  currency: string;
  cash_balance_micro_units: string;
  promotional_balance_micro_units: string;
  overdraft_limit_micro_units: string;
  status: string;
  version: string;
  updated_at: string;
};

export type StorageEstimate = {
  capacity_bytes: string;
  hourly_estimate_micro_units: string;
  currency: string;
  pricing_schedule_code: string;
  pricing_schedule_id: string;
  pricing_schedule_version_id: string;
  pricing_version: number;
  pricing_checksum: string;
  pricing_effective_from: string;
  estimated_at: string;
};

export type HypervisorEstimate = {
  cpu_cores: string;
  memory_mib: string;
  disk_gib: string;
  vcpu_hourly_micro_units: string;
  memory_hourly_micro_units: string;
  disk_hourly_micro_units: string;
  hourly_estimate_micro_units: string;
  monthly_730_hour_estimate_micro_units: string;
  currency: string;
  estimated_at: string;
};

export type MailEstimate = {
  recipient_quantity: string;
  estimate_micro_units: string;
  currency: string;
  pricing_schedule_code: string;
  pricing_schedule_id: string;
  pricing_schedule_version_id: string;
  pricing_version: number;
  pricing_checksum: string;
  pricing_effective_from: string;
  estimated_at: string;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function requiredString(record: Record<string, unknown>, key: string): string {
  const value = record[key];
  if (typeof value !== "string" || value.length === 0) {
    throw new Error(`Invalid wallet summary field: ${key}`);
  }
  return value;
}

function decodeWalletSummary(value: unknown): PersonalWalletSummary {
  if (!isRecord(value)) throw new Error("Invalid wallet summary response.");
  return {
    wallet_id: requiredString(value, "wallet_id"),
    currency: requiredString(value, "currency"),
    cash_balance_micro_units: requiredString(value, "cash_balance_micro_units"),
    promotional_balance_micro_units: requiredString(value, "promotional_balance_micro_units"),
    overdraft_limit_micro_units: requiredString(value, "overdraft_limit_micro_units"),
    status: requiredString(value, "status"),
    version: requiredString(value, "version"),
    updated_at: requiredString(value, "updated_at"),
  };
}

function requiredFiniteNumber(record: Record<string, unknown>, key: string): number {
  const value = record[key];
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) {
    throw new Error(`Invalid estimate field: ${key}`);
  }
  return value;
}

function decodeStorageEstimate(value: unknown): StorageEstimate {
  if (!isRecord(value)) throw new Error("Invalid storage estimate response.");
  return {
    capacity_bytes: requiredString(value, "capacity_bytes"),
    hourly_estimate_micro_units: requiredString(value, "hourly_estimate_micro_units"),
    currency: requiredString(value, "currency"),
    pricing_schedule_code: requiredString(value, "pricing_schedule_code"),
    pricing_schedule_id: requiredString(value, "pricing_schedule_id"),
    pricing_schedule_version_id: requiredString(value, "pricing_schedule_version_id"),
    pricing_version: requiredFiniteNumber(value, "pricing_version"),
    pricing_checksum: requiredString(value, "pricing_checksum"),
    pricing_effective_from: requiredString(value, "pricing_effective_from"),
    estimated_at: requiredString(value, "estimated_at"),
  };
}

function decodeHypervisorEstimate(value: unknown): HypervisorEstimate {
  if (!isRecord(value)) throw new Error("Invalid Hypervisor estimate response.");
  return {
    cpu_cores: requiredString(value, "cpu_cores"),
    memory_mib: requiredString(value, "memory_mib"),
    disk_gib: requiredString(value, "disk_gib"),
    vcpu_hourly_micro_units: requiredString(value, "vcpu_hourly_micro_units"),
    memory_hourly_micro_units: requiredString(value, "memory_hourly_micro_units"),
    disk_hourly_micro_units: requiredString(value, "disk_hourly_micro_units"),
    hourly_estimate_micro_units: requiredString(value, "hourly_estimate_micro_units"),
    monthly_730_hour_estimate_micro_units: requiredString(value, "monthly_730_hour_estimate_micro_units"),
    currency: requiredString(value, "currency"),
    estimated_at: requiredString(value, "estimated_at"),
  };
}

function decodeMailEstimate(value: unknown): MailEstimate {
  if (!isRecord(value)) throw new Error("Invalid Mail estimate response.");
  return {
    recipient_quantity: requiredString(value, "recipient_quantity"),
    estimate_micro_units: requiredString(value, "estimate_micro_units"),
    currency: requiredString(value, "currency"),
    pricing_schedule_code: requiredString(value, "pricing_schedule_code"),
    pricing_schedule_id: requiredString(value, "pricing_schedule_id"),
    pricing_schedule_version_id: requiredString(value, "pricing_schedule_version_id"),
    pricing_version: requiredFiniteNumber(value, "pricing_version"),
    pricing_checksum: requiredString(value, "pricing_checksum"),
    pricing_effective_from: requiredString(value, "pricing_effective_from"),
    estimated_at: requiredString(value, "estimated_at"),
  };
}

/** The API derives the personal owner from the trusted edge identity. */
export async function getPersonalWalletSummary(signal?: AbortSignal): Promise<PersonalWalletSummary> {
  const response = await fetchJSON<{ data?: unknown }>("/api/v1/billing/wallet/summary", { signal });
  return decodeWalletSummary(response.data);
}

export async function getStorageEstimate(capacityBytes: string, signal?: AbortSignal): Promise<StorageEstimate> {
  const query = new URLSearchParams({ capacity_bytes: capacityBytes });
  const response = await fetchJSON<{ data?: unknown }>(`/api/v1/billing/wallet/estimate/storage?${query}`, { signal });
  return decodeStorageEstimate(response.data);
}

export async function getHypervisorEstimate(
  cpuCores: string,
  memoryMIB: string,
  diskGIB: string,
  signal?: AbortSignal,
): Promise<HypervisorEstimate> {
  const query = new URLSearchParams({ cpu_cores: cpuCores, memory_mib: memoryMIB, disk_gib: diskGIB });
  const response = await fetchJSON<{ data?: unknown }>(`/api/v1/billing/wallet/estimate/hypervisor?${query}`, { signal });
  return decodeHypervisorEstimate(response.data);
}

export async function getMailEstimate(recipientQuantity: string, signal?: AbortSignal): Promise<MailEstimate> {
  const query = new URLSearchParams({ recipient_quantity: recipientQuantity });
  const response = await fetchJSON<{ data?: unknown }>(`/api/v1/billing/wallet/estimate/mail?${query}`, { signal });
  return decodeMailEstimate(response.data);
}
