"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
	joinTenantInvitation,
	previewTenantInvitation,
	type TenantInvitationPreview,
} from "@/features/tenants/api";

export function JoinTenantInvitationScreen({ token }: { token: string }) {
	const router = useRouter();
	const [preview, setPreview] = useState<TenantInvitationPreview | null>(null);
	const [error, setError] = useState(() => token ? "" : "This invitation link is invalid.");
	const [joining, setJoining] = useState(false);

	useEffect(() => {
		if (!token) return;
		const controller = new AbortController();
		void previewTenantInvitation(token, controller.signal)
			.then(setPreview)
			.catch((reason) => setError(reason instanceof Error ? reason.message : "This invitation is unavailable."));
		return () => controller.abort();
	}, [token]);

	return <div className="mx-auto max-w-xl">
		<Card>
			<CardHeader>
				<CardTitle>{preview ? `Join ${preview.tenant_name}` : "Tenant invitation"}</CardTitle>
				<CardDescription>{error || (preview ? `${preview.inviter_name} invited you as ${preview.role_name}.` : "Checking this invitation…")}</CardDescription>
			</CardHeader>
			{preview && <CardContent className="space-y-4">
				<div className="rounded-md border p-4 text-sm">
					<p><span className="text-muted-foreground">Tenant code:</span> {preview.tenant_code}</p>
					<p><span className="text-muted-foreground">Role:</span> {preview.role_name} (level {preview.role_level})</p>
					<p><span className="text-muted-foreground">Expires:</span> {new Date(preview.expires_at).toLocaleString()}</p>
				</div>
				<Button disabled={joining} onClick={async () => {
					setJoining(true);
					try {
						const joined = await joinTenantInvitation(token);
						toast.success(`Joined ${joined.tenant_name}.`);
						router.replace("/personal/tenants");
					} catch (reason) {
						toast.error(reason instanceof Error ? reason.message : "Cannot join this tenant.");
					} finally {
						setJoining(false);
					}
				}}>{joining ? "Joining…" : "Join tenant"}</Button>
			</CardContent>}
		</Card>
	</div>;
}
