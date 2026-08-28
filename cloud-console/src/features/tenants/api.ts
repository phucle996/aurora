import { fetchJSON } from "@/shared/api/http";
import { criticalFetchJSON } from "@/shared/api/critical";

export type CreateTenantPayload = {
  name: string;
  code: string;
  primary_domain: string;
};

export type CreateTenantResponse = {
	id: string;
	code: string;
	name: string;
	status: string;
	primary_domain: string;
};

export type TenantCatalogItem = {
	id: string;
	code: string;
	name: string;
	primary_domain: string;
	role_name: string;
	role_level: number;
};

export async function listTenants(signal?: AbortSignal): Promise<TenantCatalogItem[]> {
	const response = await fetchJSON<{ data?: { tenants?: TenantCatalogItem[] } }>("/api/v1/tenants", { signal });
	return Array.isArray(response.data?.tenants) ? response.data.tenants : [];
}

export async function switchToTenant(tenant: TenantCatalogItem): Promise<void> {
	await fetchJSON("/api/v1/context/go-to-tenant?tenant_id=" + encodeURIComponent(tenant.id) + "&tenant_domain=" + encodeURIComponent(tenant.primary_domain), {
		method: "POST",
	});
}

export async function switchToPersonal(): Promise<void> {
	await fetchJSON("/api/v1/context/go-to-personal", { method: "POST" });
}

export async function createTenant(payload: CreateTenantPayload) {
	const response = await criticalFetchJSON<{ data?: CreateTenantResponse }>("/api/v1/critical/tenants", {
		method: "POST",
		body: payload,
	});
	if (!response.data) throw new Error("The tenant response is invalid.");
	return response.data;
}

export type TenantRole = {
	id: string;
	code: string;
	name: string;
	description: string;
	role_level: number;
	version: number;
	assignments_count: number;
	outdated_assignments_count: number;
	permissions_count: number;
	created_at: string;
};

export type TenantPermission = {
	id: string;
	module: string;
	object: string;
	behavior: string;
	description: string;
};

export type TenantRoleDetails = TenantRole & { permissions: TenantPermission[] };

export type TenantRoleInput = {
	name: string;
	description: string;
	role_level: number;
	permission_ids: string[];
};

export type TenantInvitation = {
	id: string;
	tenant_role_id: string;
	role_code: string;
	role_name: string;
	role_version: number;
	expires_at: string;
	join_link: string;
};

export type TenantInvitationPreview = {
	tenant_id: string;
	tenant_code: string;
	tenant_name: string;
	inviter_name: string;
	role_code: string;
	role_name: string;
	role_level: number;
	role_version: number;
	expires_at: string;
};

export type JoinedTenant = {
	tenant_id: string;
	tenant_code: string;
	tenant_name: string;
	tenant_role_id: string;
	role_code: string;
	role_name: string;
	role_level: number;
};

export async function listTenantRoles(signal?: AbortSignal): Promise<TenantRole[]> {
	const response = await fetchJSON<{ data?: { roles?: TenantRole[] } }>("/api/v1/iam/rbac/role", { signal });
	return Array.isArray(response.data?.roles) ? response.data.roles : [];
}

export async function listTenantPermissions(signal?: AbortSignal): Promise<TenantPermission[]> {
	const response = await fetchJSON<{ data?: { permissions?: TenantPermission[] } }>("/api/v1/iam/rbac/permissions", { signal });
	return Array.isArray(response.data?.permissions) ? response.data.permissions : [];
}

export async function getTenantRole(roleID: string, signal?: AbortSignal): Promise<TenantRoleDetails> {
	const response = await fetchJSON<{ data?: { role?: TenantRoleDetails } }>(`/api/v1/iam/rbac/role/${encodeURIComponent(roleID)}`, { signal });
	if (!response.data?.role) throw new Error("The tenant role response is invalid.");
	return response.data.role;
}

export async function createTenantRole(code: string, input: TenantRoleInput): Promise<void> {
	await criticalFetchJSON("/api/v1/critical/iam/rbac/role", { method: "POST", body: { code, ...input } });
}

export async function createTenantRoleRevision(roleID: string, expectedVersion: number, input: TenantRoleInput): Promise<void> {
	await criticalFetchJSON(`/api/v1/critical/iam/rbac/role/${encodeURIComponent(roleID)}`, {
		method: "PUT",
		body: { expected_version: expectedVersion, ...input },
	});
}

export async function upgradeTenantRoleAssignments(roleID: string): Promise<{ version: number; updated_assignments_count: number }> {
	const response = await criticalFetchJSON<{ data?: { version: number; updated_assignments_count: number } }>(
		`/api/v1/critical/iam/rbac/role/${encodeURIComponent(roleID)}/assignments/upgrade`,
		{ method: "POST" },
	);
	if (!response.data) throw new Error("The rollout response is invalid.");
	return response.data;
}

export async function createTenantInvitation(identifier: string, tenantRoleID: string): Promise<TenantInvitation> {
	const response = await criticalFetchJSON<{ data?: TenantInvitation }>(
		"/api/v1/critical/hierarchy/tenant-invitations",
		{ method: "POST", body: { identifier, tenant_role_id: tenantRoleID } },
	);
	if (!response.data) throw new Error("The invitation response is invalid.");
	return response.data;
}

export async function previewTenantInvitation(token: string, signal?: AbortSignal): Promise<TenantInvitationPreview> {
	const response = await fetchJSON<{ data?: TenantInvitationPreview }>(
		`/api/v1/me/hierarchy/tenant-invitations/preview?token=${encodeURIComponent(token)}`,
		{ signal },
	);
	if (!response.data) throw new Error("The invitation response is invalid.");
	return response.data;
}

export async function joinTenantInvitation(token: string): Promise<JoinedTenant> {
	const response = await criticalFetchJSON<{ data?: JoinedTenant }>(
		"/api/v1/me/critical/hierarchy/tenant-invitations/join",
		{ method: "POST", body: { token } },
	);
	if (!response.data) throw new Error("The join response is invalid.");
	return response.data;
}
