import { fetchJSON } from "./fetcher";

export type CreateTenantPayload = {
  name: string;
  code: string;
  primary_domain: string;
};

export type CreateTenantResponse = {
  tenant_id: string;
  domain: string;
};

export async function createTenant(payload: CreateTenantPayload) {
  return fetchJSON<CreateTenantResponse>("/api/v1/tenants", {
    method: "POST",
    body: payload,
  });
}
