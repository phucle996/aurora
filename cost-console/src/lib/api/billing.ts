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
  zone_code: string;
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
  zone_code: string;
  unit: string;
  unit_price: number;
  currency: string;
  tier: string;
  free_quota: number;
  effective_from: string;
  effective_to?: string;
  created_at: string;
}

const API_BASE = import.meta.env.VITE_BILLING_API_URL || 'http://localhost:8084/api/v1/billing';

// Đảm bảo có owner_id duy nhất trong session để đối soát/subscribe thử nghiệm
export function getDemoOwner(): { id: string; type: string } {
  let id = localStorage.getItem('demo_owner_id');
  if (!id) {
    id = '019f3d3e-0000-7894-9236-c5122634cb4f'; // Default demo UUID
    localStorage.setItem('demo_owner_id', id);
  }
  return { id, type: 'personal' };
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const url = `${API_BASE}${path}`;
  const response = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options?.headers || {}),
    },
  });

  if (!response.ok) {
    const errorText = await response.text();
    let errorJson;
    try {
      errorJson = JSON.parse(errorText);
    } catch {
      // Ignored
    }
    throw new Error(errorJson?.message || errorJson?.error || `HTTP error ${response.status}`);
  }

  const resJson = await response.json();
  return resJson.data as T;
}

export const billingApi = {
  // Get active subscription for current demo owner
  async getActiveSubscription(): Promise<Subscription | null> {
    const owner = getDemoOwner();
    try {
      return await request<Subscription | null>(
        `/subscriptions/active?owner_id=${owner.id}&owner_type=${owner.type}`
      );
    } catch (e) {
      console.error('Failed to fetch active subscription:', e);
      return null;
    }
  },

  // Subscribe current demo owner to a plan
  async subscribe(planId: string): Promise<Subscription> {
    const owner = getDemoOwner();
    return await request<Subscription>('/subscriptions', {
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
    await request<void>(`/subscriptions/active?owner_id=${owner.id}&owner_type=${owner.type}`, {
      method: 'DELETE',
    });
  },

  // List all available active plans
  async listPlans(): Promise<PlanItem[]> {
    return await request<PlanItem[]>('/plans');
  },

  // Create a new plan (Admin feature)
  async createPlan(plan: Omit<PlanItem, 'id' | 'status'>): Promise<PlanItem> {
    return await request<PlanItem>('/plans', {
      method: 'POST',
      body: JSON.stringify(plan),
    });
  },

  // Update status of plan (Admin feature)
  async updatePlanStatus(planId: string, status: 'ACTIVE' | 'DEPRECATED'): Promise<void> {
    await request<void>(`/plans/${planId}/status`, {
      method: 'PATCH',
      body: JSON.stringify({ status }),
    });
  },

  // List real zones
  async listZones(): Promise<ZoneItem[]> {
    return await request<ZoneItem[]>('/zones');
  },

  // List all prices
  async listPrices(): Promise<PriceItem[]> {
    return await request<PriceItem[]>('/prices');
  }
};
