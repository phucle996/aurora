import { JoinTenantInvitationScreen } from "@/features/tenants/join-invitation-screen";

export default async function JoinTenantInvitationPage({ searchParams }: { searchParams: Promise<{ token?: string }> }) {
	const { token } = await searchParams;
	return <JoinTenantInvitationScreen token={token ?? ""} />;
}
