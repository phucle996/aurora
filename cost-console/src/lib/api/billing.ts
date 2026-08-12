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
export type PricingScope = 'GLOBAL' | 'ZONE';

export interface PricingSchedule {
  id: string;
  code: string;
  display_name: string;
  charge_kind_code: string;
  pricing_model: PricingModel;
  scope_type: PricingScope;
  zone_id?: string;
  currency: string;
  metadata_version: number;
  status: 'ACTIVE' | 'DISABLED';
  created_at: string;
  updated_at: string;
}

export interface PricingBracket {
  id?: string;
  range_start_quantity: number;
  range_end_quantity: number | null;
  price_numerator_micro_units: number;
  price_denominator_quantity: number;
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

export interface PricingScheduleDetail {
  id: string;
  code: string;
  display_name: string;
  charge_kind_code: string;
  pricing_model: PricingModel;
  scope_type: PricingScope;
  zone_id?: string;
  currency: string;
  metadata_version: number;
  latest_version: PricingScheduleVersion;
}

export interface PricingSchedulesResponse {
  pricing_schedules: PricingSchedule[];
  pagination: { page: number; limit: number; total: number };
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

  async updatePricingScheduleMetadata(code: string, payload: { metadata_version: number; display_name: string }): Promise<PricingSchedule> {
    return criticalFetcher<PricingSchedule>(`/billing/critical/pricing-schedules/${encodeURIComponent(code)}/metadata`, {
      method: 'PATCH',
      body: payload,
    });
  },

  async publishPricingScheduleVersion(code: string, payload: {
    expected_latest_version: number;
    effective_from: string;
    change_reason: string;
    brackets: PricingBracket[];
  }): Promise<PricingScheduleVersion> {
    return criticalFetcher<PricingScheduleVersion>(`/billing/critical/pricing-schedules/${encodeURIComponent(code)}/versions`, {
      method: 'POST',
      body: payload,
    });
  },
};
