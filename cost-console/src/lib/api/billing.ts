import { request } from './fetcher';
import { criticalFetcher } from './criticalFetcher';

export interface PlanMetric {
  id: string;
  plan_id: string;
  metric_type: string;
  quota: number;
  unit: string;
}

export interface PlanItem {
  id: string;
  name: string;
  code: string;
  service_type: string;
  zone_id: string;
  monthly_price: number;
  currency: string;
  status: string;
  description: string;
  metrics?: PlanMetric[];
  created_at?: string;
}

export interface ZoneItem {
  id: string;
  code: string;
  name: string;
  status: string;
}

export interface PriceItem {
  id: string;
  service_type: string;
  metric_type: string;
  zone_id: string;
  unit: string;
  unit_price: number;
  currency: string;
  tier: string;
  free_quota: number;
  effective_from: string;
  effective_to?: string;
  created_at: string;
}

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
      {
        method: 'PATCH',
        body: { status, expected_version: expectedVersion },
      },
    );
  },

  // List all available active plans
  async listPlans(): Promise<PlanItem[]> {
    return await request<PlanItem[]>('/billing/plans');
  },

  // Create a new plan (Admin feature)
  async createPlan(plan: Omit<PlanItem, 'id' | 'status'>): Promise<PlanItem> {
    return await request<PlanItem>('/billing/plans', {
      method: 'POST',
      body: JSON.stringify(plan),
    });
  },

  // Update status of plan (Admin feature)
  async updatePlanStatus(planId: string, status: 'ACTIVE' | 'DEPRECATED'): Promise<void> {
    await request<void>(`/billing/plans/${planId}/status`, {
      method: 'PATCH',
      body: JSON.stringify({ status }),
    });
  },

  // List real zones
  async listZones(): Promise<ZoneItem[]> {
    return await request<ZoneItem[]>('/billing/zones');
  },

  // List all prices
  async listPrices(): Promise<PriceItem[]> {
    return await request<PriceItem[]>('/billing/prices');
  },

  // [COMMENT]: Gọi API lấy danh sách biểu giá cước lũy tiến (Tiers) có phân trang và bộ lọc
  async listTiers(
    page: number = 1,
    limit: number = 10,
    serviceType?: string,
    search?: string
  ): Promise<TiersResponse> {
    let url = `/billing/tiers?page=${page}&limit=${limit}`;
    if (serviceType && serviceType !== 'all') {
      url += `&service_type=${serviceType}`;
    }
    if (search) {
      url += `&search=${encodeURIComponent(search)}`;
    }
    return await request<TiersResponse>(url);
  },

  // [COMMENT]: Luôn load full latest aggregate trước Edit, không dựng snapshot từ flat paginated rows.
  async getTierDetail(code: string, serviceType: string): Promise<TierDetail> {
    return await request<TierDetail>(
      `/billing/tiers/${encodeURIComponent(serviceType)}/${encodeURIComponent(code)}`
    );
  },

  // [COMMENT]: Name dùng metadata OCC riêng và không phát pricing version/outbox.
  async updateTierMetadata(payload: UpdateTierMetadataPayload): Promise<TierMetadataResult> {
    const { code, service_type, ...body } = payload;
    return await criticalFetcher<TierMetadataResult>(
      `/billing/critical/tiers/${encodeURIComponent(service_type)}/${encodeURIComponent(code)}/metadata`,
      {
        method: 'PATCH',
        body,
      }
    );
  },

  // [COMMENT]: Pricing edit append immutable full snapshot; không gửi range IDs cũ để mutate lịch sử.
  async createTierVersion(payload: CreateTierVersionPayload): Promise<TierVersion> {
    const { code, service_type, ...body } = payload;
    return await criticalFetcher<TierVersion>(
      `/billing/critical/tiers/${encodeURIComponent(service_type)}/${encodeURIComponent(code)}/versions`,
      {
        method: 'POST',
        body,
      }
    );
  }
};

// [COMMENT]: Interface đại diện cho dòng biểu giá cước lũy tiến chi tiết dạng phẳng (Flat Tier)
export interface TierItem {
  id: string;              // ID của nấc cước chi tiết (Range ID)
  tier_id: string;         // ID của biểu giá gốc (Tier ID)
  name: string;            // Tên biểu giá gốc (VD: Standard Storage Base Tier)
  code: string;            // Mã biểu giá gốc (VD: STORAGE_STD_BASE)
  service_type: string;    // Loại dịch vụ (STORAGE | NETWORK_IN | NETWORK_OUT)
  metadata_version: number;
  pricing_version: number;
  range_start: number;     // Mốc bắt đầu (Megabytes - MB)
  range_end: number;       // Mốc kết thúc (MB), 0 biểu thị không giới hạn (vô cực)
  base_unit_price: number; // Giá gốc (USD Micro-units/MB/Hour)
  created_at: string;
  updated_at: string;
}

export interface TierRangeInput {
  id?: string;
  range_start: number;
  range_end: number;
  base_unit_price: number;
}

export interface TierVersion {
  id: string;
  tier_id: string;
  version_number: number;
  status: 'SCHEDULED' | 'ACTIVE' | 'SUPERSEDED' | 'CANCELLED';
  effective_from: string;
  effective_to?: string | null;
  checksum: string;
  ranges: TierRangeInput[];
}

export interface TierDetail {
  id: string;
  code: string;
  service_type: string;
  name: string;
  metadata_version: number;
  latest_version: TierVersion;
}

export interface UpdateTierMetadataPayload {
  code: string;
  service_type: string;
  metadata_version: number;
  name: string;
}

export interface TierMetadataResult extends UpdateTierMetadataPayload {
  id: string;
  updated_at: string;
}

export interface CreateTierVersionPayload {
  code: string;
  service_type: string;
  expected_latest_version: number;
  effective_from: string;
  change_reason: string;
  ranges: Array<Omit<TierRangeInput, 'id'>>;
}

// [COMMENT]: Cấu trúc Response phân trang từ API GET /tiers
export interface TiersResponse {
  tiers: TierItem[];
  pagination: {
    page: number;
    limit: number;
    total: number;
  };
}
