import { criticalFetcher } from './criticalFetcher';
import { request } from './fetcher';

export interface WalletSummary {
  wallet_id: string;
  currency: string;
  cash_balance_micro_units: string;
  promotional_balance_micro_units: string;
  overdraft_limit_micro_units: string;
  status: 'PENDING_ACTIVATION' | 'ACTIVE' | 'SUSPENDED' | 'CLOSED';
  version: string;
  updated_at: string;
  minimum_top_up_micro_units?: string;
}

export interface ReferralReservation {
  id: string;
  code: string;
  status: 'RESERVED' | 'REDEEMED' | 'REJECTED' | 'CANCELLED';
  grant_amount_micro_units: string;
  minimum_top_up_micro_units: string;
  currency: string;
  expires_at: string;
  redeemed_at?: string;
  rejection_reason?: string;
}

export interface PaymentIntent {
  id: string;
  amount_micro_units: string;
  currency: string;
  status: 'PENDING' | 'SETTLED' | 'EXPIRED' | 'CANCELLED';
  activates_wallet: boolean;
  expires_at: string;
  created_at: string;
  settled_at?: string;
  checkout_url?: string;
}

export interface BillingOnboarding {
  wallet: WalletSummary;
  minimum_top_up_micro_units: string;
  referral: ReferralReservation | null;
  latest_payment_intent: PaymentIntent | null;
}

export interface ReferralCampaign {
  id: string;
  code: string;
  name: string;
  amount_micro_units: string;
  minimum_top_up_micro_units: string;
  currency: string;
  status: 'ACTIVE' | 'PAUSED' | 'ENDED';
  max_redemptions?: string;
  redemptions: string;
  active_reservations: string;
  version: string;
  starts_at: string;
  ends_at?: string;
  created_at: string;
  updated_at: string;
}

export type PricingModel = 'PROGRESSIVE_UNIT' | 'FIXED_BUNDLE';

export interface PricingSchedule {
  id: string;
  code: string;
  display_name: string;
  charge_kind_code: string;
  pricing_model: PricingModel;
  currency: string;
  metadata_version: number;
  status: 'ACTIVE' | 'DISABLED';
  created_at: string;
  updated_at: string;
}

export interface PricingBracket {
  id?: string;
  range_start_quantity: string;
  range_end_quantity: string | null;
  price_numerator_micro_units: string;
  price_denominator_quantity: string;
}

export interface PricingScheduleVersion {
  id: string;
  pricing_schedule_id: string;
  version_number: number;
  pricing_model: PricingModel;
  status: 'SCHEDULED' | 'ACTIVE' | 'SUPERSEDED' | 'CANCELLED';
  effective_from: string;
  effective_to?: string | null;
  checksum: string;
  brackets: PricingBracket[];
}

export interface PublishedBasePriceVersion {
	id: string;
	pricing_schedule_id: string;
	charge_kind_code: string;
	version_number: number;
	pricing_model: PricingModel;
	status: 'SCHEDULED' | 'ACTIVE';
	effective_from: string;
	effective_to: null;
	checksum: string;
}

export interface PricingScheduleDetail {
  id: string;
  code: string;
  display_name: string;
  charge_kind_code: string;
  pricing_model: PricingModel;
  currency: string;
  metadata_version: number;
  latest_version: PricingScheduleVersion | null;
}

export interface PricingScheduleRateStateVersion extends PricingScheduleVersion {
  change_reason: string;
}

export interface PricingScheduleRateState {
  id: string;
  code: string;
  display_name: string;
  charge_kind_code: string;
  pricing_model: PricingModel;
  currency: string;
  metadata_version: number;
  observed_at: string;
  latest_version_number: number | null;
  effective_version: PricingScheduleRateStateVersion | null;
  next_scheduled_version: PricingScheduleRateStateVersion | null;
}

export interface PricingSchedulesResponse {
  pricing_schedules: PricingSchedule[];
  pagination: { page: number; limit: number; total: number };
}

export interface ZoneCatalogEntry {
  code: string;
  name: string;
}

export interface MailZonePriceAdjustment {
  id: string;
  zone_id: string;
  version_number: number;
  status: 'SCHEDULED' | 'ACTIVE' | 'SUPERSEDED' | 'CANCELLED';
  effective_from: string;
  effective_to: string | null;
  multiplier_numerator: string;
  multiplier_denominator: string;
  checksum: string;
  change_reason: string;
  created_by: string;
  created_at: string;
  is_latest: boolean;
  is_effective: boolean;
}

export interface MailZonePriceAdjustmentsResponse {
  zone_id: string;
  adjustments: MailZonePriceAdjustment[];
  has_more: boolean;
  observed_at: string;
}

export interface PublishedMailZonePriceAdjustment {
  id: string;
  zone_id: string;
  version_number: number;
  status: 'SCHEDULED' | 'ACTIVE';
  effective_from: string;
  effective_to: null;
  multiplier_numerator: string;
  multiplier_denominator: string;
  checksum: string;
}

export interface StorageZonePriceAdjustment {
  id: string;
  zone_id: string;
  version_number: number;
  status: 'SCHEDULED' | 'ACTIVE' | 'SUPERSEDED' | 'CANCELLED';
  effective_from: string;
  effective_to: string | null;
  multiplier_numerator: string;
  multiplier_denominator: string;
  checksum: string;
  change_reason: string;
  created_by: string;
  created_at: string;
  is_latest: boolean;
  is_effective: boolean;
}

export interface StorageZonePriceAdjustmentsResponse {
  zone_id: string;
  adjustments: StorageZonePriceAdjustment[];
  has_more: boolean;
  observed_at: string;
}

export interface PublishedStorageZonePriceAdjustment {
  id: string;
  zone_id: string;
  version_number: number;
  status: 'SCHEDULED' | 'ACTIVE';
  effective_from: string;
  effective_to: null;
  multiplier_numerator: string;
  multiplier_denominator: string;
  checksum: string;
}

export interface HypervisorZonePriceAdjustment {
	id: string;
	zone_id: string;
	version_number: number;
	status: 'SCHEDULED' | 'ACTIVE' | 'SUPERSEDED' | 'CANCELLED';
	effective_from: string;
	effective_to: string | null;
	multiplier_numerator: string;
	multiplier_denominator: string;
	checksum: string;
	change_reason: string;
	created_by: string;
	created_at: string;
	is_latest: boolean;
	is_effective: boolean;
}

export interface HypervisorZonePriceAdjustmentsResponse {
	zone_id: string;
	adjustments: HypervisorZonePriceAdjustment[];
	has_more: boolean;
	observed_at: string;
}

export interface PublishedHypervisorZonePriceAdjustment {
	id: string;
	zone_id: string;
	version_number: number;
	status: 'SCHEDULED' | 'ACTIVE';
	effective_from: string;
	effective_to: null;
	multiplier_numerator: string;
	multiplier_denominator: string;
	checksum: string;
}

export interface HypervisorResourcePlan {
	plan_id: string;
	revision_id: string;
	revision_number: string;
	code: string;
	display_name: string;
	description: string;
	billing_model: 'LIMIT_HOURLY';
	cpu_cores: string;
	memory_mib: string;
	boot_disk_gib: string;
	content_sha256: string;
	effective_from: string;
	effective_to: string | null;
}

export interface HypervisorResourcePlanAdminItem {
	plan_id: string;
	code: string;
	display_name: string;
	description: string;
	state: string;
	latest_revision_number: string;
	effective_revision_number: string;
}
export interface HypervisorResourcePlanHistoryItem {
	plan_id: string;
	revision_id: string;
	revision_number: string;
	cpu_cores: string;
	memory_mib: string;
	boot_disk_gib: string;
	effective_from: string;
	effective_to: string | null;
	state: string;
	change_reason: string;
	is_latest: boolean;
	is_effective: boolean;
}
export interface HypervisorResourcePlansResponse {
	plans: HypervisorResourcePlanAdminItem[];
	next_cursor: string;
	observed_at: string;
}
export interface HypervisorResourcePlanHistoryResponse {
	revisions: HypervisorResourcePlanHistoryItem[];
	next_cursor: string;
	observed_at: string;
}

export const billingApi = {
  async getWalletSummary(signal?: AbortSignal): Promise<WalletSummary> {
    return request<WalletSummary>('/billing/wallet/summary', { method: 'GET', signal });
  },

  async getOnboarding(signal?: AbortSignal): Promise<BillingOnboarding> {
    return request<BillingOnboarding>('/billing/wallet/onboarding', { method: 'GET', signal });
  },

  async reserveReferral(code: string, idempotencyKey: string): Promise<ReferralReservation> {
    return request<ReferralReservation>('/billing/wallet/referral', {
      method: 'POST',
      headers: { 'idempotency-key': idempotencyKey },
      body: JSON.stringify({ code }),
    });
  },

  async createTopUp(amountMicroUnits: string, idempotencyKey: string): Promise<PaymentIntent> {
    return request<PaymentIntent>('/billing/wallet/top-ups', {
      method: 'POST',
      headers: { 'idempotency-key': idempotencyKey },
      body: JSON.stringify({ amount_micro_units: amountMicroUnits }),
    });
  },

  async getTopUp(id: string, signal?: AbortSignal): Promise<PaymentIntent> {
    return request<PaymentIntent>(`/billing/wallet/top-ups/${encodeURIComponent(id)}`, { method: 'GET', signal });
  },

  async listReferralCampaigns(signal?: AbortSignal): Promise<ReferralCampaign[]> {
    return request<ReferralCampaign[]>('/billing/referrals', { method: 'GET', signal });
  },

  async createReferralCampaign(payload: {
    code: string;
    name: string;
    amount_micro_units: string;
    minimum_top_up_micro_units: string;
    currency: 'USD';
    max_redemptions?: string;
    starts_at: string;
    ends_at?: string;
  }): Promise<ReferralCampaign> {
    return criticalFetcher<ReferralCampaign>('/billing/critical/referrals', {
      method: 'POST',
      body: payload,
    });
  },

  async updateReferralCampaignStatus(
    id: string,
    status: ReferralCampaign['status'],
    expectedVersion: string,
  ): Promise<ReferralCampaign> {
    return criticalFetcher<ReferralCampaign>(
      `/billing/critical/referrals/${encodeURIComponent(id)}/status`,
      { method: 'PATCH', body: { status, expected_version: expectedVersion } },
    );
  },

  async listPricingSchedules(page = 1, limit = 50, chargeKind?: string, search?: string): Promise<PricingSchedulesResponse> {
    const query = new URLSearchParams({ page: String(page), limit: String(limit) });
    if (chargeKind) query.set('charge_kind', chargeKind);
    if (search) query.set('search', search);
    return request<PricingSchedulesResponse>(`/billing/pricing-schedules?${query.toString()}`);
  },

  async getPricingScheduleDetail(code: string): Promise<PricingScheduleDetail> {
    return request<PricingScheduleDetail>(`/billing/pricing-schedules/${encodeURIComponent(code)}`);
  },

  async getPricingScheduleRateState(code: string): Promise<PricingScheduleRateState> {
    return request<PricingScheduleRateState>(`/billing/pricing-schedules/${encodeURIComponent(code)}/rate-state`);
  },

  async updatePricingScheduleMetadata(code: string, payload: { metadata_version: number; display_name: string }): Promise<PricingSchedule> {
    return criticalFetcher<PricingSchedule>(`/billing/critical/pricing-schedules/${encodeURIComponent(code)}/metadata`, {
      method: 'PATCH',
      body: payload,
    });
  },

  async publishStorageBasePriceVersion(code: string, payload: {
    expected_latest_version: number;
    effective_from: string;
    change_reason: string;
    brackets: PricingBracket[];
	}): Promise<PublishedBasePriceVersion> {
		return criticalFetcher<PublishedBasePriceVersion>(`/billing/critical/storage/pricing-schedules/${encodeURIComponent(code)}/versions`, {
			method: 'POST',
			body: payload,
		});
	},

	async publishHypervisorBasePriceVersion(code: string, payload: {
		expected_latest_version: number;
		effective_from: string;
		change_reason: string;
		brackets: PricingBracket[];
	}): Promise<PublishedBasePriceVersion> {
		return criticalFetcher<PublishedBasePriceVersion>(`/billing/critical/hypervisor/pricing-schedules/${encodeURIComponent(code)}/versions`, {
			method: 'POST',
			body: payload,
		});
	},

	async publishMailBasePriceVersion(code: string, payload: {
		expected_latest_version: number;
		effective_from: string;
		change_reason: string;
		brackets: PricingBracket[];
	}): Promise<PublishedBasePriceVersion> {
		return criticalFetcher<PublishedBasePriceVersion>(`/billing/critical/mail/pricing-schedules/${encodeURIComponent(code)}/versions`, {
			method: 'POST',
			body: payload,
		});
	},

  async listZoneCatalog(): Promise<ZoneCatalogEntry[]> {
    return request<ZoneCatalogEntry[]>('/zones/catalog');
  },

  async listMailZonePriceAdjustments(zoneCode: string, limit = 100, signal?: AbortSignal): Promise<MailZonePriceAdjustmentsResponse> {
    const query = new URLSearchParams({ limit: String(limit), zone_code: zoneCode });
    return request<MailZonePriceAdjustmentsResponse>(`/billing/mail/zone-price-adjustments?${query.toString()}`, {
      method: 'GET',
      signal,
    });
  },

	async listStorageZonePriceAdjustments(zoneCode: string, limit = 100, signal?: AbortSignal): Promise<StorageZonePriceAdjustmentsResponse> {
    const query = new URLSearchParams({ limit: String(limit), zone_code: zoneCode });
    return request<StorageZonePriceAdjustmentsResponse>(`/billing/storage/zone-price-adjustments?${query.toString()}`, {
      method: 'GET',
      signal,
    });
	},

	async listHypervisorZonePriceAdjustments(zoneCode: string, limit = 100, signal?: AbortSignal): Promise<HypervisorZonePriceAdjustmentsResponse> {
		const query = new URLSearchParams({ limit: String(limit), zone_code: zoneCode });
		return request<HypervisorZonePriceAdjustmentsResponse>(`/billing/hypervisor/zone-price-adjustments?${query.toString()}`, {
			method: 'GET',
			signal,
		});
	},

  async publishStorageZonePriceAdjustment(zoneCode: string, payload: {
    expected_latest_version: number;
    effective_from: string;
    change_reason: string;
    multiplier_numerator: string;
    multiplier_denominator: string;
  }): Promise<PublishedStorageZonePriceAdjustment> {
    return criticalFetcher<PublishedStorageZonePriceAdjustment>(`/billing/critical/storage/zone-price-adjustments/versions?zone_code=${encodeURIComponent(zoneCode)}`, {
      method: 'POST',
      body: payload,
    });
  },

	async publishMailZonePriceAdjustment(zoneCode: string, payload: {
    expected_latest_version: number;
    effective_from: string;
    change_reason: string;
    multiplier_numerator: string;
    multiplier_denominator: string;
  }): Promise<PublishedMailZonePriceAdjustment> {
		return criticalFetcher<PublishedMailZonePriceAdjustment>(`/billing/critical/mail/zone-price-adjustments/versions?zone_code=${encodeURIComponent(zoneCode)}`, {
      method: 'POST',
      body: payload,
		});
	},

	async publishHypervisorZonePriceAdjustment(zoneCode: string, payload: {
		expected_latest_version: number;
		effective_from: string;
		change_reason: string;
		multiplier_numerator: string;
		multiplier_denominator: string;
	}): Promise<PublishedHypervisorZonePriceAdjustment> {
		return criticalFetcher<PublishedHypervisorZonePriceAdjustment>(`/billing/critical/hypervisor/zone-price-adjustments/versions?zone_code=${encodeURIComponent(zoneCode)}`, {
			method: 'POST',
			body: payload,
		});
	},

	async listHypervisorResourcePlans(limit = 50, signal?: AbortSignal, after = ''): Promise<HypervisorResourcePlansResponse> {
		const query = new URLSearchParams({ limit: String(limit) });
		if (after) query.set('after', after);
		return request<HypervisorResourcePlansResponse>(`/billing/hypervisor/resource-plans?${query}`, { method: 'GET', signal });
	},

	async listHypervisorResourcePlanRevisions(planID: string, signal?: AbortSignal, before = ''): Promise<HypervisorResourcePlanHistoryResponse> {
		const query = new URLSearchParams({ limit: '50' });
		if (before) query.set('before', before);
		return request<HypervisorResourcePlanHistoryResponse>(`/billing/hypervisor/resource-plans/${encodeURIComponent(planID)}/revisions?${query}`, { method: 'GET', signal });
	},

	async createHypervisorResourcePlan(payload: {
		code: string;
		display_name: string;
		description: string;
		cpu_cores: string;
		memory_mib: string;
		boot_disk_gib: string;
		effective_from: string;
		change_reason: string;
	}): Promise<HypervisorResourcePlan> {
		return criticalFetcher<HypervisorResourcePlan>('/billing/critical/hypervisor/resource-plans', { method: 'POST', body: payload });
	},

	async publishHypervisorResourcePlanRevision(planID: string, payload: {
		expected_latest_revision: string;
		cpu_cores: string;
		memory_mib: string;
		boot_disk_gib: string;
		effective_from: string;
		change_reason: string;
	}): Promise<HypervisorResourcePlan> {
		return criticalFetcher<HypervisorResourcePlan>(`/billing/critical/hypervisor/resource-plans/${encodeURIComponent(planID)}/revisions`, { method: 'POST', body: payload });
	},
};
