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
  monthly_estimate_micro_units: string;
  billing_hours_per_month: number;
  currency: string;
  tier_code: string;
  tier_id: string;
  tier_version_id: string;
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
    monthly_estimate_micro_units: requiredString(value, "monthly_estimate_micro_units"),
    billing_hours_per_month: requiredFiniteNumber(value, "billing_hours_per_month"),
    currency: requiredString(value, "currency"),
    tier_code: requiredString(value, "tier_code"),
    tier_id: requiredString(value, "tier_id"),
    tier_version_id: requiredString(value, "tier_version_id"),
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
