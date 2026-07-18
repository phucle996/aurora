import { request } from './fetcher';

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

export interface Subscription {
  id: string;
  owner_id: string;
  owner_type: string;
  plan_id: string;
  status: string;
  started_at: string;
  expires_at?: string;
  plan?: PlanItem;
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

// Đảm bảo có owner_id duy nhất trong session để đối soát/subscribe thử nghiệm
export function getDemoOwner(): { id: string; type: string } {
  let id = localStorage.getItem('demo_owner_id');
  if (!id) {
    id = '019f3d3e-0000-7894-9236-c5122634cb4f'; // Default demo UUID
    localStorage.setItem('demo_owner_id', id);
  }
  return { id, type: 'personal' };
}

export const billingApi = {
  // Get active subscription for current demo owner
  async getActiveSubscription(): Promise<Subscription | null> {
    const owner = getDemoOwner();
    try {
      return await request<Subscription | null>(
        `/billing/subscriptions/active?owner_id=${owner.id}&owner_type=${owner.type}`
      );
    } catch (e) {
      console.error('Failed to fetch active subscription:', e);
      return null;
    }
  },

  // Subscribe current demo owner to a plan
  async subscribe(planId: string): Promise<Subscription> {
    const owner = getDemoOwner();
    return await request<Subscription>('/billing/subscriptions', {
      method: 'POST',
      body: JSON.stringify({
        owner_id: owner.id,
        owner_type: owner.type,
        plan_id: planId,
      }),
    });
  },

  // Cancel subscription for current demo owner
  async cancelSubscription(): Promise<void> {
    const owner = getDemoOwner();
    await request<void>(`/billing/subscriptions/active?owner_id=${owner.id}&owner_type=${owner.type}`, {
      method: 'DELETE',
    });
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
    return await request<TierMetadataResult>(
      `/billing/tiers/${encodeURIComponent(service_type)}/${encodeURIComponent(code)}/metadata`,
      {
        method: 'PATCH',
        body: JSON.stringify(body),
      }
    );
  },

  // [COMMENT]: Pricing edit append immutable full snapshot; không gửi range IDs cũ để mutate lịch sử.
  async createTierVersion(payload: CreateTierVersionPayload): Promise<TierVersion> {
    const { code, service_type, ...body } = payload;
    return await request<TierVersion>(
      `/billing/tiers/${encodeURIComponent(service_type)}/${encodeURIComponent(code)}/versions`,
      {
        method: 'POST',
        body: JSON.stringify(body),
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
